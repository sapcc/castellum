# castellum

Vertical autoscaling service for OpenStack.

TODO describe more

## Environment variables

| Variable | Default | Explanation |
| -------- | ------- | ----------- |
| `CASTELLUM_DB_URI` | *(required)* | A [libpq connection URI][pq-uri] that locates the Limes database. The non-URI "connection string" format is not allowed; it must be a URI. |

[pq-uri]: https://www.postgresql.org/docs/9.6/static/libpq-connect.html#LIBPQ-CONNSTRING
