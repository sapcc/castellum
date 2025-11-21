<!--
SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company

SPDX-License-Identifier: Apache-2.0
-->

# Asset manager: `nfs-shares`

The asset manager `nfs-shares` provides asset types for resizing NFS shares
managed by [OpenStack Manila](https://wiki.openstack.org/wiki/Manila).

* The asset type `nfs-shares` matches all Manila shares in the respective project.

## User considerations

### Resource configuration

The Castellum API does not accept any additional configuration for `nfs-shares` resources.

## Operational considerations

Because Manila only reports the size of its shares, not their usage, the `nfs-shares` asset manager also reads
Prometheus metrics emitted by the [netapp-api-exporter](https://github.com/sapcc/netapp-api-exporter). The following
metrics are expected:

```
# shares are discovered using this query (share ID is expected to be stored in the label `id`)
openstack_manila_shares_size_gauge{project_id="...",status!="error"}

# then, for shares where this query does not yield any results...
manila_share_exclusion_reasons_for_castellum{share_id="...",project_id="...",reason!=""} == 1

# ...size and usage data is retrieved from these metrics
manila_share_minimal_size_bytes_for_castellum{volume_type!="dp",volume_state!="offline",share_id="...",project_id="..."}
manila_share_size_bytes_for_castellum{volume_type!="dp",volume_state!="offline",share_id="...",project_id="..."}
manila_share_used_bytes_for_castellum{volume_type!="dp",volume_state!="offline",share_id="...",project_id="..."}
```

Discovery of Manila shares also happens via Prometheus because Prometheus queries are usually faster at scale than
Manila API queries.

If you want Castellum to ignore a Manila share, fill the `manila_share_exclusion_reasons_for_castellum` metric as
described above.

If any of the aforementioned metric families do not contain any entries, Castellum will assume that this is because of a problem with metric scraping.
Castellum will then rather throw errors instead of silently proceeding on the assumption that there are no shares.
For the exclusion-reasons metric family specifically, it may be necessary to add dummy metrics when there are legitimately no excluded shares.

### Configuration

| Variable | Default | Explanation |
| -------- | ------- | ----------- |
| `CASTELLUM_NFS_PROMETHEUS_URL` | *(required)* | The URL of the Prometheus instance providing the `manila_share_..._for_castellum` metrics (see above), e.g. `https://prometheus.example.org:9090`. |
| `CASTELLUM_NFS_PROMETHEUS_CACERT` | *(optional)* | A CA certificate that the Prometheus instance's server certificate is signed by (only when HTTPS is used). Only required if the CA certificate is not included in the system-wide CA bundle. |
| `CASTELLUM_NFS_PROMETHEUS_CERT` | *(optional)* | A client certificate to present to the Prometheus instance (only when HTTPS is used). |
| `CASTELLUM_NFS_PROMETHEUS_KEY` | *(optional)* | The private key for the aforementioned client certificate. |
| `CASTELLUM_NFS_DISCOVERY_PROMETHEUS_URL`<br>`CASTELLUM_NFS_DISCOVERY_PROMETHEUS_CACERT`<br>`CASTELLUM_NFS_DISCOVERY_PROMETHEUS_CERT`<br>`CASTELLUM_NFS_DISCOVERY_PROMETHEUS_KEY` | *(required)*<br>*(optional)*<br>*(optional)*<br>*(optional)* | A similar set of configuration variables for finding and connecting to the Prometheus instance providing the `openstack_manila_shares_size_gauge` metric (see above). May be identical to the former set. |

### Required permissions

The Castellum service user must be able to list, extend and shrink Manila shares in all projects.

### Policy considerations

- `project:show:nfs-shares` can usually be given to everyone who can interact with Manila shares.
- `project:edit:nfs-shares` should only be given to users who can extend and shrink Manila shares.
