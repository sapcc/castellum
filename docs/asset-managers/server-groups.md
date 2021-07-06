# Asset manager: `server-groups`

The asset manager `server-groups` provides one asset type of the form `server-group:$UUID` for each
[Nova server group](https://docs.openstack.org/api-ref/compute/#server-groups-os-server-groups). Each resource with
such an asset type contains exactly one asset, the server group itself. The asset UUID is the server group ID.

Scaling is performed horizontally: Upscaling launches new instances from the template given in the [resource
configuration](../api-spec.md#server-group), and downscaling terminates instances.

Usage information is collected from Prometheus, by querying the metrics `vrops_virtualmachine_{cpu_usage,memory_active}_ratio` as emitted by the [vrops-exporter](https://github.com/sapcc/vrops-exporter).

## Configuration

| Variable | Default | Explanation |
| -------- | ------- | ----------- |
| `CASTELLUM_SERVERGROUPS_PROMETHEUS_URL` | *(required)* | The URL of the Prometheus instance providing usage metrics to this asset manager, e.g. `https://prometheus.example.org:9090`. |
| `CASTELLUM_SERVERGROUPS_LOCAL_ROLES` | *(required)* | A comma-separated list of role names. [See
below](#required-permissions) for details. |

## Required permissions

The Castellum service user must be able to assign Keystone roles to itself in all projects. To create and terminate
instances in any given project, the Castellum service user assigns the roles listed in the
`CASTELLUM_SERVERGROUPS_LOCAL_ROLES` environment variable to itself before authenticating into the project scope. That
set of roles therefore must be sufficient for performing all required lookups and operations related to creating and
terminating instances (including lookups for images, flavors, Barbican secrets, etc.).

## Policy considerations

- `project:show:server-group` can be given to everyone who has read access to Nova instances in the project.
- `project:edit:server-group` should only be granted to users who can create instances and read Barbican secrets.
