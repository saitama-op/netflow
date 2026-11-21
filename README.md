# NetFlow Exporter

A Go service that receives NetFlow v5, NetFlow v9, and IPFIX over UDP, attributes
flows to WAN interfaces and internal device groups, and exposes Prometheus
metrics. It decodes packets directly; a separate GoFlow2 process and log pipe
are not needed.

## Requirements

- Go 1.26.8 or newer
- A router or exporter configured to send NetFlow/IPFIX packets to this service
- `/etc/netflow-exporter/groups.json`

The service needs permission to bind its UDP and HTTP listen addresses. For
ports below 1024, run with suitable privileges or grant the binary permission
to bind low ports.

## Build and run

```sh
go build -o netflow-exporter .
sudo install -D -m 0644 groups.json /etc/netflow-exporter/groups.json
./netflow-exporter \
  -netflow-addr :2055 \
  -port :9202 \
  -exporter-ip 192.168.10.238
```

Configure the router to send NetFlow v5/v9 or IPFIX datagrams to the host's UDP
port `2055`. Ensure UDP `2055` and the Prometheus HTTP port are reachable from
the appropriate networks.

For IPFIX, the exporter learns templates from the sender. Configure the sender
to periodically export templates to the same UDP destination. If a data set
arrives before its template, that data set is skipped; other data sets in the
same packet with known templates can still be processed. Subsequent data sets
are decoded once their template arrives. Missing template IDs are logged once
while unresolved; records skipped before template arrival are not replayed.

By default, the exporter listens on UDP `:2055` and serves Prometheus metrics
on `:9202`. The old JSON-on-stdin input remains available as an additional
input with `-stdin`; it does not disable UDP collection.

## Command-line flags

| Flag | Default | Description |
| --- | --- | --- |
| `-netflow-addr` | `:2055` | UDP address for NetFlow v5, v9, and IPFIX packets |
| `-port` | `:9202` | HTTP address for the Prometheus metrics endpoint |
| `-exporter-ip` | `192.168.10.238` | Local exporter IP, used to identify its own traffic; must be a valid IP address |
| `-stdin` | `false` | Also read newline-delimited JSON flow objects from standard input |

Use `-h` to display flag help.

## Running with systemd

The service binds both listeners before entering its run loop. On `SIGTERM` or
`SIGINT`, it stops accepting NetFlow packets, flushes pending flow
correlations, and gracefully shuts down the HTTP server.

Example `/etc/systemd/system/netflow-exporter.service`:

```ini
[Unit]
Description=NetFlow Prometheus Exporter
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=netflow
Group=netflow
ExecStart=/usr/local/bin/netflow-exporter -netflow-addr :2055 -port :9202 -exporter-ip 192.168.10.238
Restart=on-failure
RestartSec=5
TimeoutStopSec=60

[Install]
WantedBy=multi-user.target
```

Ensure the `netflow` account can read `/etc/netflow-exporter/groups.json` and
execute the binary. Then enable and start the service:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now netflow-exporter
sudo systemctl status netflow-exporter
```

## Device groups

The service loads groups and input-interface mappings from
`/etc/netflow-exporter/groups.json` at startup. The `groups` object maps group
names to arrays of individual IPv4 addresses or inclusive IPv4 ranges. The
`interfaces` object maps input-interface indices to a direction and, for
downloads, an ISP name:

```json
{
  "groups": {
    "office": [
      "192.168.1.10",
      "192.168.1.20-192.168.1.40"
    ],
    "servers": [
      "192.168.2.0-192.168.2.255"
    ]
  },
  "interfaces": {
    "2": { "direction": "upload" },
    "3": { "direction": "download", "isp_name": "HATHWAY" },
    "4": { "direction": "download", "isp_name": "ACT" },
    "5": { "direction": "download", "isp_name": "AIRTEL" }
  }
}
```

Set `direction` to `upload` or `download`. Each download interface must include
an `isp_name`; upload interfaces do not require one. Unconfigured interface
indices are ignored. The example preserves the previous built-in interface
mapping, which you can adjust to match your router:

| Input interface | Attribution |
| --- | --- |
| `2` | LAN upload |
| `3` | Download via HATHWAY |
| `4` | Download via ACT |
| `5` | Download via AIRTEL |

Addresses not present in the groups are assigned to the `UNKNOWN` group.

## Prometheus metrics

Scrape `http://<host>:9202/metrics` (or the address configured with `-port`).

- `netflow_bytes_total` — flow bytes
- `netflow_flows_total` — number of flow records

Both metrics use the labels `sampler`, `proto`, `interface`, `group`,
`direction`, and `client_ip`. `direction` is `upload` or `download`. Unresolved
device or ISP correlations are queued briefly and then exported with an
`UNKNOWN` attribution where necessary.

## Legacy JSON input

When `-stdin` is enabled, the process also reads JSON flow objects from stdin.
Each object has this shape:

```json
{
  "sampler_address": "192.168.10.1",
  "src_addr": "192.168.1.10",
  "dst_addr": "203.0.113.20",
  "proto": "TCP",
  "src_port": 49152,
  "dst_port": 443,
  "bytes": 1200,
  "in_if": 2
}
```

This mode can be used during migration from an existing flow pipeline; direct
UDP collection does not require it.
