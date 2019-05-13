# Asset manager: `nfs-shares`

The asset manager `nfs-shares` provides an asset type with the same name for resizing NFS shares
managed by [OpenStack Manila](https://wiki.openstack.org/wiki/Manila). Because Manila only reports
the size of its shares, not their usage, the `nfs-shares` asset manager also reads Prometheus
metrics emitted by the [netapp-api-exporter](https://github.com/sapcc/netapp-api-exporter).

## Configuration

| Variable | Default | Explanation |
| -------- | ------- | ----------- |
| `CASTELLUM_NFS_PROMETHEUS_URL` | *(required)* | The URL of the Prometheus instance providing usage metrics to this asset manager, e.g. `https://prometheus.example.org:9090`. |
