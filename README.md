# castellum

Vertical autoscaling service for OpenStack.

TODO describe more

## Environment variables

| Variable | Default | Explanation |
| -------- | ------- | ----------- |
| `CASTELLUM_DB_URI` | *(required)* | A [libpq connection URI][pq-uri] that locates the Limes database. The non-URI "connection string" format is not allowed; it must be a URI. |
| `OS_...` | *(required)* | A full set of OpenStack auth environment variables. See [documentation for openstackclient][os-env] for details. |

[pq-uri]: https://www.postgresql.org/docs/9.6/static/libpq-connect.html#LIBPQ-CONNSTRING
[os-env]: https://docs.openstack.org/python-openstackclient/latest/cli/man/openstack.html
