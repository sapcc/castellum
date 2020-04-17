# Asset manager: `nfs-shares`

The asset manager `nfs-shares` provides an asset type with the same name for resizing NFS shares
managed by [OpenStack Manila](https://wiki.openstack.org/wiki/Manila). Because Manila only reports
the size of its shares, not their usage, the `nfs-shares` asset manager also reads Prometheus
metrics emitted by the [netapp-api-exporter](https://github.com/sapcc/netapp-api-exporter).

## Configuration

| Variable | Default | Explanation |
| -------- | ------- | ----------- |
| `CASTELLUM_NFS_PROMETHEUS_URL` | *(required)* | The URL of the Prometheus instance providing usage metrics to this asset manager, e.g. `https://prometheus.example.org:9090`. |

## Required permissions

The Castellum service user must be able to list, extend and shrink Manila shares in all projects.

## Policy considerations

- `project:show:nfs-shares` can usually be given to everyone who can interact with Manila shares.
- `project:edit:nfs-shares` should only be given to users who can extend and shrink Manila shares.

## Hooks

If you want Castellum to ignore a Manila share, you can set the metadata key `snapmirror` to value `1`, e.g.

    manila metadata SHARE_ID set snapmirror=1

Usually you want that, if your share is a target in a NetApp SnapMirror setup. Size modification is anyhow not possible in this case.
