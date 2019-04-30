# castellum

Castellum is a vertical autoscaling service for OpenStack. It can perform autonomous resize operations, upscaling assets
with a high usage and downscaling assets with a low usage.

In this document:

* [Terminology](#terminology)
* [Building and running](#building-and-running)
  * [Supported asset types](#supported-asset-types)
  * [Prometheus metrics](#prometheus-metrics)

In other documents:

* [API specification](./docs/api-spec.md)
* [Notes for developers/contributors](./CONTRIBUTING.md)

## Terminology

- An **asset** is a thing that Castellum can resize. Assets have a size and usage, such that `0 <= usage <= size`.
  - example: "NFS share 2180c598-58f3-4d1d-b03e-303db22de1be"
- A **resource** is the sum of all assets in a certain authentication scope. Castellum's behavior is configured at this
  level, e.g. thresholds and resizing steps. See [*API specification*](./docs/api-spec.md) for details.
  - example: "NFS shares in project 5ceb23209bef4292b9ec97eb3e664f74"
- An **operation** is a single resize performed by Castellum.

Operations move through the following states:

![State machine](./docs/state-machine.png)

- *Created*: The asset's usage has crossed one of the thresholds configured on the resource.
- *Confirmed*: The asset's usage has stayed at problematic levels for the configured delay. (For the critical threshold,
  there is no delay, so operations move from "created" to "confirmed" automatically.)
- *Greenlit*: The operation has been approved by a user. (If no approval requirement is configured, operations move from
  "confirmed" into "greenlit" automatically.)
- *Cancelled*: While an operation was not yet greenlit, the asset's usage moved back to normal levels.
- *Succeeded*: The resize operation was completed successfully.
- *Failed*: The resize operation was attempted, but failed.

## Building and running

Build with `make && make install`, or with `docker build` if that's to your taste. Castellum has three different
components that you all need to run for a complete installation:

- `castellum api` provides an OpenStack-style HTTP-based REST API. To add TLS, put this behind a reverse proxy.
- `castellum observer` discovers assets and (based on their status) creates, confirms and cancels resize operations.
- `castellum worker` performs the actual resizing.

The API and worker components can be scaled horizontally at will. **The observer cannot be scaled**. Do not run more
than one instance of it at a time.

All components receive configuration via environment variables. The following variables are recognized:

| Variable | Default | Explanation |
| -------- | ------- | ----------- |
| `CASTELLUM_ASSET_MANAGERS` | *(required)* | A comma-separated list of all asset managers that can be enabled. This configures what kinds of assets Castellum can handle. [See below](#supported-asset-types) for which asset managers exist. |
| `CASTELLUM_DB_URI` | *(required)* | A [libpq connection URI][pq-uri] that locates the Limes database. The non-URI "connection string" format is not allowed; it must be a URI. |
| `CASTELLUM_HTTP_LISTEN_ADDRESS` | `:8080` | Listen address for the internal HTTP server. For `castellum observer/worker`, this just exposes Prometheus metrics on `/metrics`. For `castelum api`, this also exposes [the REST API](./docs/api-spec.md). |
| `OS_...` | *(required)* | A full set of OpenStack auth environment variables for Castellum's service user. See [documentation for openstackclient][os-env] for details. |

### Supported asset types

The following asset managers are available:

- TODO

### Prometheus metrics

Each component (API, observer and worker) exposes Prometheus metrics via HTTP, on the `/metrics` endpoint. The following metrics are exposed:

| Metric/Component | Description |
| ---------------- | ----------- |
| `castellum_operation_state_transitions`<br/>(API, observer, worker) | Counter for state transitions of operations. Labels: `asset` (asset type), `from_state` and `to_state`. |

[pq-uri]: https://www.postgresql.org/docs/9.6/static/libpq-connect.html#LIBPQ-CONNSTRING
[os-env]: https://docs.openstack.org/python-openstackclient/latest/cli/man/openstack.html
