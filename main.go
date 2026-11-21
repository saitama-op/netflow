package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const configFile = "/etc/netflow-exporter/groups.json"

func main() {
	portFlag := flag.String("port", ":9202", "HTTP port to listen on for Prometheus metrics")
	exporterIPFlag := flag.String("exporter-ip", "192.168.10.238", "IP address of the local NetFlow exporter")
	netflowAddrFlag := flag.String("netflow-addr", ":2055", "UDP address to receive NetFlow v5, v9 and IPFIX")
	stdinFlag := flag.Bool("stdin", false, "also read JSON flows from stdin")
	flag.Parse()

	if net.ParseIP(*exporterIPFlag) == nil {
		log.Fatalf("invalid exporter IP %q", *exporterIPFlag)
	}
	log.Printf("Using exporter IP %s", *exporterIPFlag)

	if err := LoadConfig(configFile); err != nil {
		log.Fatalf("failed to load configuration file %s: %v", configFile, err)
	}
	log.Printf("Loaded %d groups and %d interfaces from %s", len(Groups), len(Interfaces), configFile)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *portFlag, *netflowAddrFlag, *exporterIPFlag, *stdinFlag); err != nil {
		log.Printf("netflow exporter stopped with error: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, httpAddr, netflowAddr, exporterIP string, stdinEnabled bool) (runErr error) {
	prometheus.MustRegister(NetflowBytes)
	prometheus.MustRegister(NetflowFlows)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	server := &http.Server{
		Addr:              httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	httpListener, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return fmt.Errorf("listen for Prometheus metrics on %s: %w", httpAddr, err)
	}

	netflowListener, err := StartNetFlowListener(netflowAddr, exporterIP)
	if err != nil {
		_ = httpListener.Close()
		return fmt.Errorf("start NetFlow listener: %w", err)
	}

	workerCtx, stopWorkers := context.WithCancel(context.Background())
	cacheDone := make(chan struct{})
	queueDone := make(chan struct{})
	go func() {
		defer close(cacheDone)
		StartCacheGC(workerCtx)
	}()
	go func() {
		defer close(queueDone)
		StartQueueWorker(workerCtx)
	}()

	httpErrors := make(chan error, 1)
	go func() {
		if err := server.Serve(httpListener); err != nil && err != http.ErrServerClosed {
			httpErrors <- err
		}
	}()
	log.Printf("Prometheus metrics endpoint ready on %s", httpAddr)

	var stdinFlows <-chan Flow
	if stdinEnabled {
		stdinFlows = readJSONFlows(ctx)
	}

	defer func() {
		log.Println("shutting down NetFlow receiver")
		if err := netflowListener.Stop(); err != nil {
			log.Printf("failed to stop NetFlow receiver cleanly: %v", err)
			if runErr == nil {
				runErr = fmt.Errorf("stop NetFlow receiver: %w", err)
			}
		}

		stopWorkers()
		select {
		case <-queueDone:
		case <-time.After(20 * time.Second):
			log.Println("timed out waiting for pending flows to flush")
			<-queueDone
		}
		<-cacheDone

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful HTTP shutdown failed: %v", err)
			runErr = errors.Join(runErr, fmt.Errorf("graceful HTTP shutdown: %w", err))
			if closeErr := server.Close(); closeErr != nil {
				runErr = errors.Join(runErr, fmt.Errorf("close HTTP server: %w", closeErr))
			}
		}
		log.Println("shutdown complete")
	}()

	for {
		select {
		case <-ctx.Done():
			log.Printf("received shutdown signal: %v", ctx.Err())
			return nil
		case err := <-httpErrors:
			return fmt.Errorf("Prometheus HTTP server: %w", err)
		case err := <-netflowListener.Errors():
			if IsNetFlowDecodeError(err) {
				log.Printf("discarding invalid NetFlow packet: %v", err)
				continue
			}
			return fmt.Errorf("NetFlow UDP receiver: %w", err)
		case flow, ok := <-stdinFlows:
			if !ok {
				log.Println("JSON stdin input closed; continuing UDP collection")
				stdinFlows = nil
				continue
			}
			flow.Proto = strings.ToUpper(strings.TrimSpace(flow.Proto))
			if flow.Bytes > 0 {
				ProcessFlow(flow, exporterIP)
			}
		}
	}
}

func readJSONFlows(ctx context.Context) <-chan Flow {
	flows := make(chan Flow)
	go func() {
		defer close(flows)
		decoder := json.NewDecoder(os.Stdin)
		for {
			var flow Flow
			if err := decoder.Decode(&flow); err != nil {
				if err != io.EOF {
					log.Printf("failed to decode JSON stdin input: %v", err)
				}
				return
			}
			select {
			case flows <- flow:
			case <-ctx.Done():
				return
			}
		}
	}()
	return flows
}

func ProcessFlow(flow Flow, exporterIP string) {
	var publicIP, internalIP, ispName, direction, connKey string
	iface, ok := Interfaces[flow.InIf]
	if !ok {
		return
	}

	if iface.Direction == "upload" {
		direction = "upload"
		internalIP = flow.SrcAddr
		publicIP = flow.DstAddr

		if IsInternalTraffic(publicIP) || IsLocalExporterIP(internalIP, exporterIP) {
			ispName = "INTERNAL"
			connKey = "internal_traffic"
		} else {
			connKey = fmt.Sprintf("%s:%s:%d", flow.Proto, publicIP, flow.SrcPort)
			Cache.setDevice(connKey, internalIP)
			ispName = Cache.getISP(connKey)
		}
	} else if iface.Direction == "download" {
		direction = "download"
		publicIP = flow.SrcAddr
		ispName = iface.ISPName
		connKey = fmt.Sprintf("%s:%s:%d", flow.Proto, publicIP, flow.DstPort)

		Cache.setISP(connKey, ispName)
		internalIP = Cache.getDevice(connKey)

		if internalIP == "" {
			if DeviceGroup(flow.DstAddr) != "UNKNOWN" {
				internalIP = flow.DstAddr
			} else if flow.Proto == "ICMP" || flow.Proto == "IPv6-ICMP" || flow.Proto == "IGMP" {
				internalIP = "192.168.0.2"
			} else {
				internalIP = "UNKNOWN_DEVICE"
			}
		}
	} else {
		return
	}

	group := DeviceGroup(internalIP)
	if (direction == "upload" && ispName == "") || (direction == "download" && internalIP == "UNKNOWN_DEVICE") {
		EnqueuePending(PendingFlow{
			Flow:       flow,
			ConnKey:    connKey,
			InternalIP: internalIP,
			ISPName:    ispName,
			Direction:  direction,
			Group:      group,
			ExpiresAt:  time.Now().Add(15 * time.Second),
		})
		return
	}

	ExportMetric(flow, direction, ispName, group, internalIP, connKey)
}
