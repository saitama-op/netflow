package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"

	goflowproducer "github.com/netsampler/goflow2/v2/producer"
	protoproducer "github.com/netsampler/goflow2/v2/producer/proto"
	"github.com/netsampler/goflow2/v2/utils"
	"github.com/netsampler/goflow2/v2/utils/templates"
)

type noopFormat struct{}

func (noopFormat) Format(data interface{}) ([]byte, []byte, error) {
	return nil, nil, nil
}

type noopTransport struct{}

func (noopTransport) Send(key, data []byte) error {
	return nil
}

type netFlowReceiverCallback struct{}

func (netFlowReceiverCallback) Dropped(msg utils.Message) {
	log.Printf("dropped NetFlow packet from %s", msg.Src)
}

type exporterProducer struct {
	underlying goflowproducer.ProducerInterface
	exporterIP string
}

type NetFlowListener struct {
	receiver *utils.UDPReceiver
	producer goflowproducer.ProducerInterface
	stopOnce sync.Once
	stopErr  error
}

func (l *NetFlowListener) Errors() <-chan error {
	return l.receiver.Errors()
}

func (l *NetFlowListener) Stop() error {
	l.stopOnce.Do(func() {
		l.stopErr = l.receiver.Stop()
		l.producer.Close()
	})
	return l.stopErr
}

func (p *exporterProducer) Produce(msg interface{}, args *goflowproducer.ProduceArgs) ([]goflowproducer.ProducerMessage, error) {
	return p.underlying.Produce(msg, args)
}

func (p *exporterProducer) Commit(messages []goflowproducer.ProducerMessage) {
	for _, message := range messages {
		flowMessage, ok := message.(*protoproducer.ProtoProducerMessage)
		if !ok {
			continue
		}

		srcAddr := net.IP(flowMessage.SrcAddr).String()
		dstAddr := net.IP(flowMessage.DstAddr).String()
		flow := Flow{
			SamplerAddress: net.IP(flowMessage.SamplerAddress).String(),
			SrcAddr:        srcAddr,
			DstAddr:        dstAddr,
			Proto:          protocolName(flowMessage.Proto),
			SrcPort:        int(flowMessage.SrcPort),
			DstPort:        int(flowMessage.DstPort),
			Bytes:          float64(flowMessage.Bytes),
			InIf:           int(flowMessage.InIf),
		}
		ProcessFlow(flow, p.exporterIP)
	}
	p.underlying.Commit(messages)
}

func (p *exporterProducer) Close() {
	p.underlying.Close()
}

func protocolName(proto uint32) string {
	switch proto {
	case 1:
		return "ICMP"
	case 2:
		return "IGMP"
	case 6:
		return "TCP"
	case 17:
		return "UDP"
	case 58:
		return "IPv6-ICMP"
	default:
		return strconv.FormatUint(uint64(proto), 10)
	}
}

func StartNetFlowListener(address, exporterIP string) (*NetFlowListener, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid NetFlow listen address %q: %w", address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid NetFlow UDP port %q", portText)
	}

	producerConfig := &protoproducer.ProducerConfig{}
	compiledConfig, err := producerConfig.Compile()
	if err != nil {
		return nil, fmt.Errorf("compile NetFlow producer: %w", err)
	}
	underlyingProducer, err := protoproducer.CreateProtoProducer(
		compiledConfig,
		protoproducer.CreateSamplingSystem,
	)
	if err != nil {
		return nil, err
	}
	wrappedProducer := &exporterProducer{underlying: underlyingProducer, exporterIP: exporterIP}

	pipe := utils.NewNetFlowPipe(&utils.PipeConfig{
		Format:           noopFormat{},
		Transport:        noopTransport{},
		Producer:         wrappedProducer,
		NetFlowTemplater: templates.DefaultTemplateGenerator,
	})

	receiver, err := utils.NewUDPReceiver(&utils.UDPReceiverConfig{
		Workers:          1,
		Sockets:          1,
		Blocking:         true,
		QueueSize:        10000,
		ReceiverCallback: netFlowReceiverCallback{},
	})
	if err != nil {
		wrappedProducer.Close()
		return nil, err
	}

	if err := receiver.Start(host, port, func(message interface{}) error {
		packet, ok := message.(*utils.Message)
		if !ok {
			return pipe.DecodeFlow(message)
		}
		return decodeNetFlowMessage(pipe, packet)
	}); err != nil {
		wrappedProducer.Close()
		return nil, err
	}

	log.Printf("NetFlow UDP listener ready on %s", address)
	return &NetFlowListener{receiver: receiver, producer: wrappedProducer}, nil
}

func IsNetFlowDecodeError(err error) bool {
	var decodeErr *utils.PipeMessageError
	return errors.As(err, &decodeErr)
}
