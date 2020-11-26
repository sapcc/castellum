# Castellum

[![Build Status](https://travis-ci.org/sapcc/castellum.svg?branch=master)](https://travis-ci.org/sapcc/castellum)
[![Coverage Status](https://coveralls.io/repos/github/sapcc/castellum/badge.svg?branch=master)](https://coveralls.io/github/sapcc/castellum?branch=master)
[![Go Report Card](https://goreportcard.com/badge/github.com/sapcc/castellum)](https://goreportcard.com/report/github.com/sapcc/castellum)

Castellum is a vertical autoscaling service for OpenStack. It can perform autonomous resize operations, upscaling assets
with a high usage and downscaling assets with a low usage.

In this document:

* [Terminology](#terminology)
* [Building and running](#building-and-running)
  * [Oslo policy](#oslo-policy)
  * [Prometheus metrics](#prometheus-metrics)

In other documents:

* [Supported asset types](./docs/asset-managers/)
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
- *Failed*/*Errored*: The resize operation was attempted, but failed or errored.

Problems with resizing fall into two categories: **Failures** need to be addressed by the project/domain administrators
(e.g. upsize failed because of insufficient quota), while **errors** are unexpected backend errors that the OpenStack
administrator needs to take care of (e.g. outage of an API used by Castellum).

## Building and running

Build with `make && make install`, or with `docker build` if that's to your taste. Castellum has three different
components that you all need to run for a complete installation:

- `castellum api` provides an OpenStack-style HTTP-based REST API. To add TLS, put this behind a reverse proxy.
- `castellum observer` discovers assets and (based on their status) creates, confirms and cancels resize operations.
- `castellum worker` performs the actual resizing.

The API and worker components can be scaled horizontally at will. **The observer cannot be scaled**. Do not run more
than one instance of it at a time.

The API component has audit trail support and can be configured to send audit events to a RabbitMQ server.

All components receive configuration via environment variables. The following variables are recognized:

| Variable | Default | Explanation |
| -------- | ------- | ----------- |
| `CASTELLUM_ASSET_MANAGERS` | *(required)* | A comma-separated list of all asset managers that can be enabled. This configures what kinds of assets Castellum can handle. See [`docs/asset-managers/`](./docs/asset-managers/) for which asset managers exist. |
| `CASTELLUM_DB_USERNAME` | `postgres` | Username of the user that Castellum should use to connect to the database. |
| `CASTELLUM_DB_PASSWORD` | *(optional)* | Password for the specified user. |
| `CASTELLUM_DB_HOSTNAME` | `localhost` | Hostname of the database server. |
| `CASTELLUM_DB_PORT` | `5432` | Port on which the PostgreSQL service is running on. |
| `CASTELLUM_DB_NAME` | `castellum` | The name of the database. |
| `CASTELLUM_DB_CONNECTION_OPTIONS` | `sslmode=disable` | Database connection options. |
| `CASTELLUM_HTTP_LISTEN_ADDRESS` | `:8080` | Listen address for the internal HTTP server. For `castellum observer/worker`, this just exposes Prometheus metrics on `/metrics`. For `castelum api`, this also exposes [the REST API](./docs/api-spec.md). |
| `CASTELLUM_MAX_ASSET_SIZES` | *(optional)* | A comma-separated list of `<asset-type>=<max-size>` pairs. If present, only resource configurations honoring these constraints will be allowed. |
| `CASTELLUM_OSLO_POLICY_PATH` | *(required)* | Path to the `policy.json` file for this service. See [*Oslo policy*](#oslo-policy) for details. |
| `CASTELLUM_RABBITMQ_QUEUE_NAME` | *(required for enabling audit trail)* | Name for the queue that will hold the audit events. The events are published to the default exchange. |
| `CASTELLUM_RABBITMQ_USERNAME` | *(optional)* | RabbitMQ Username. |
| `CASTELLUM_RABBITMQ_PASSWORD` | *(optional)* | Password for the specified user. |
| `CASTELLUM_RABBITMQ_HOSTNAME` | *(optional)* | Hostname of the RabbitMQ server. |
| `CASTELLUM_RABBITMQ_PORT` | `5672` |  Port number to which the underlying connection is made. |
| `CASTELLUM_AUDIT_SILENT` | `false` | Disable audit event logging to standard output. |
| `CASTELLUM_LOG_SCRAPES` | `false` | Whether to write a log line for each asset scrape operation. This can be useful to debug situations where Castellum does not create operations when it should, but it generates a lot of log traffic (one line per asset per 5 minutes, which e.g. for 2000 assets is about 1 GiB per week). |
| `OS_...` | *(required)* | A full set of OpenStack auth environment variables for Castellum's service user. See [documentation for openstackclient][os-env] for details. |

### Oslo policy

Castellum understands access rules in the [`oslo.policy` JSON format][os-pol]. An example can be seen at
[`docs/example-policy.json`](./docs/example-policy.json). The following rules are expected:

- `project:access` gates access to all endpoints relating to a project, even if more specific rules are checked later on.
- `project:show:<asset_type_shortened>` gates access to all endpoints relating to a project resource.
- `project:edit:<asset_type_shortened>` gates access to the PUT and DELETE endpoints relating to a project resource.

All project-level policy rules can use the following object attributes:

```
%(project_id)s           <- deprecated, use the next one instead
%(target.project.id)s
%(target.project.name)s
%(target.project.domain.id)s
%(target.project.domain.name)s
```

When policy rule names reference the asset type, only the part of the asset type up until the first colon is used. For
example, access to project resources with asset type `project-quota:compute:instances` would be gated by the rules
`project:show:project-quota` and `project:edit:project-quota`.

See also: [List of available API attributes](https://github.com/sapcc/go-bits/blob/53eeb20fde03c3d0a35e76cf9c9a06b63a415e6b/gopherpolicy/pkg.go#L151-L164)

### Prometheus metrics

Each component (API, observer and worker) exposes Prometheus metrics via HTTP, on the `/metrics` endpoint. The following metrics are exposed:

| Metric/Component | Description |
| ---------------- | ----------- |
| `castellum_operation_state_transitions`<br/>(API, observer, worker) | Counter for state transitions of operations.<br/>Labels: `project_id`, `asset` (asset type), `from_state` and `to_state`. |
| `castellum_has_project_resource`<br/>(observer) | Constant value of 1 for each existing project resource. This can be used in alert expressions to distinguish resources with autoscaling from resources without autoscaling.<br/>Labels: `project_id`, `asset` (asset type). |
| `castellum_successful_resource_scrapes`<br/>(observer) | Counter for successful resource scrape operations.<br/>Labels: `asset` (asset type). |
| `castellum_failed_resource_scrapes`<br/>(observer) | Counter for failed resource scrape operations.<br/>Labels: `asset` (asset type). |
| `castellum_successful_asset_scrapes`<br/>(observer) | Counter for successful asset scrape operations.<br/>Labels: `asset` (asset type). |
| `castellum_failed_asset_scrapes`<br/>(observer) | Counter for failed asset scrape operations.<br/>Labels: `asset` (asset type). |
| `castellum_asset_resizes`<br/>(worker) | Counter for asset resize operations that ran to completion, i.e. which consumed a PendingOperation and produced a FinishedOperation in either "succeeded" or "failed" state.<br/>Labels: `asset` (asset type). |
| `castellum_errored_asset_resizes`<br/>(worker) | Counter for asset resize operations that encountered an unexpected error, i.e. which could not consume a PendingOperation and produce a FinishedOperation.<br/>Labels: `asset` (asset type). |

Note that `castellum_asset_resizes` is also incremented for resize operations that move into state "failed". The counter
`castellum_errored_asset_resizes` is only incremented when a greenlit operation cannot be moved out of the "greenlit"
state at all. Resize operations that move into state "failed" are counted by `castellum_operation_state_transitions{to_state="failed"}`.

[pq-uri]: https://www.postgresql.org/docs/9.6/static/libpq-connect.html#LIBPQ-CONNSTRING
[os-env]: https://docs.openstack.org/python-openstackclient/latest/cli/man/openstack.html
[os-pol]: https://docs.openstack.org/oslo.policy/latest/admin/policy-json-file.html
