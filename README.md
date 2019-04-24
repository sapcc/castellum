# castellum

Vertical autoscaling service for OpenStack.

TODO describe more

## Environment variables

| Variable | Default | Explanation |
| -------- | ------- | ----------- |
| `CASTELLUM_ASSET_MANAGERS` | *(required)* | A comma-separated list of all asset managers that can be enabled. This configures what kinds of assets Castellum can handle. See below the table for which asset managers exist. |
| `CASTELLUM_DB_URI` | *(required)* | A [libpq connection URI][pq-uri] that locates the Limes database. The non-URI "connection string" format is not allowed; it must be a URI. |
| `OS_...` | *(required)* | A full set of OpenStack auth environment variables for Castellum's service user. See [documentation for openstackclient][os-env] for details. |

The following asset managers are available:

- TODO

[pq-uri]: https://www.postgresql.org/docs/9.6/static/libpq-connect.html#LIBPQ-CONNSTRING
[os-env]: https://docs.openstack.org/python-openstackclient/latest/cli/man/openstack.html
