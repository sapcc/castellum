# Asset manager: `server-groups`

The asset manager `server-groups` provides one asset type of the form `server-group:$UUID` for each
[Nova server group](https://docs.openstack.org/api-ref/compute/#server-groups-os-server-groups). Each resource with
such an asset type contains exactly one asset, the server group itself. The asset UUID is the server group ID.

Scaling is performed horizontally: Upscaling launches new instances from the template given in the [resource
configuration](#resource-configuration), and downscaling terminates instances.

## User considerations

### Resource configuration

The Castellum API requires additional configuration for `server-group:*` resources:

| Field | Type | Explanation |
| ----- | ---- | ----------- |
| `delete_newest_first` | boolean | When true, downscaling will terminate the instances with the newest `created_at` timestamp. The default value is `false`, which means that downscaling will terminate the oldest instances instead. Both behaviors can make sense: Set this to true if you prefer to keep old instances that are known to work well, or leave it at false to use scaling events as an opportunity to gradually replace old instances with fresh ones. |
| `loadbalancer_pool_memberships` | array of objects | Configuration for LB pool memberships. When configured, upscaling will automatically add all new instances as members to these LB pools after creation, and downscaling will remove instances from these LB pools before deleting them. |
| `loadbalancer_pool_memberships[].pool_uuid` | string<br>*(required)* | The UUID of the pool. |
| `loadbalancer_pool_memberships[].protocol_port` | integer<br>*(required)* | The port where each instance answers load-balanced requests. |
| `loadbalancer_pool_memberships[].monitor_port` | integer | The port where the LB's health monitor will probe this instance. If set to zero, the `protocol_port` will be used for monitors, too. |
| `template` | object | Configuration for new instances that are created by upscaling operations. |
| `template.availability_zone` | string | If not empty, new instances will be created in this availability zone. |
| `template.block_device_mapping_v2` | array of objects | If given, block devices will be attached to all new instances as defined in this key. The structure of this field is identical to the respective field on [the instance creation request body in the Nova API](https://docs.openstack.org/api-ref/compute/?expanded=create-server-detail#create-server). It is **highly recommended** to set `delete_on_termination` to true on all block device mappings defined herein, to prevent left-over volumes from piling up. |
| `template.flavor.name` | string<br>*(required)* | The name of the flavor that will be used for new instances. |
| `template.image.name` | string<br>*(required)* | The name of the image that new instances will be booted with. |
| `template.metadata` | object of strings | Metadata key and value pairs that will be provided to new instances. The maximum size of keys and values is 255 bytes each. |
| `template.networks` | array of objects<br>*(required)* | Which networks the new instances will be connected to. |
| `template.networks[].uuid` | string<br>*(required)* | The ID of the network. |
| `template.networks[].tag` | string | A device role tag that can be applied to a network interface. The guest OS of a server that has devices tagged in this manner can access hardware metadata about the tagged devices from the metadata API and on the config drive, if enabled. |
| `template.public_key.barbican_uuid` | string<br>*(required)* | A UUID under which an SSH public key is stored in Barbican. This public key will be used when booting new instances. |
| `template.security_groups` | array of strings<br>*(required)* | New instances will be created in these security groups. |
| `template.user_data` | string | Configuration information or scripts to use when booting new instances. The maximum size is 65535 bytes. |

## Operational considerations

Usage information is collected from Prometheus, by querying the metrics `vrops_virtualmachine_{cpu_usage,memory_active}_ratio` as emitted by the [vrops-exporter](https://github.com/sapcc/vrops-exporter).

### Configuration

| Variable | Default | Explanation |
| -------- | ------- | ----------- |
| `CASTELLUM_SERVERGROUPS_PROMETHEUS_URL` | *(required)* | The URL of the Prometheus instance providing usage metrics to this asset manager, e.g. `https://prometheus.example.org:9090`. |
| `CASTELLUM_SERVERGROUPS_LOCAL_ROLES` | *(required)* | A comma-separated list of role names. [See
below](#required-permissions) for details. |

### Required permissions

The Castellum service user must be able to assign Keystone roles to itself in all projects. To create and terminate
instances in any given project, the Castellum service user assigns the roles listed in the
`CASTELLUM_SERVERGROUPS_LOCAL_ROLES` environment variable to itself before authenticating into the project scope. That
set of roles therefore must be sufficient for performing all required lookups and operations related to creating and
terminating instances (including lookups for images, flavors, Barbican secrets, etc.).

### Policy considerations

- `project:show:server-group` can be given to everyone who has read access to Nova instances in the project.
- `project:edit:server-group` should only be granted to users who can create instances and read Barbican secrets.
