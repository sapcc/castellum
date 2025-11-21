<!--
SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company

SPDX-License-Identifier: Apache-2.0
-->

# Castellum

[![CI](https://github.com/sapcc/castellum/actions/workflows/ci.yaml/badge.svg)](https://github.com/sapcc/castellum/actions/workflows/ci.yaml)
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

- `castellum api <config-file>` provides an OpenStack-style HTTP-based REST API. To add TLS, put this behind a reverse proxy.
- `castellum observer <config-file>` discovers assets and (based on their status) creates, confirms and cancels resize operations.
- `castellum worker <config-file>` performs the actual resizing.

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
| `CASTELLUM_DB_CONNECTION_OPTIONS` | *(optional)* | Database connection options. |
| `CASTELLUM_HTTP_LISTEN_ADDRESS` | `:8080` | Listen address for the internal HTTP server. For `castellum observer/worker`, this just exposes Prometheus metrics on `/metrics`. For `castellum api`, this also exposes [the REST API](./docs/api-spec.md). |
| `CASTELLUM_LOG_SCRAPES` | `false` | Whether to write a log line for each asset scrape operation. This can be useful to debug situations where Castellum does not create operations when it should, but it generates a lot of log traffic (one line per asset per 5 minutes, which e.g. for 2000 assets is about 1 GiB per week). |
| `CASTELLUM_OSLO_POLICY_PATH`<br>(API only) | *(required)* | Path to the `policy.json` file for this service. See [*Oslo policy*](#oslo-policy) for details. |
| `CASTELLUM_RABBITMQ_QUEUE_NAME`<br>(API only) | *(required for enabling audit trail)* | Name for the queue that will hold the audit events. The events are published to the default exchange. |
| `CASTELLUM_RABBITMQ_USERNAME`<br>(API only) | `guest` | RabbitMQ Username. |
| `CASTELLUM_RABBITMQ_PASSWORD`<br>(API only) | `guest` | Password for the specified user. |
| `CASTELLUM_RABBITMQ_HOSTNAME`<br>(API only) | `localhost` | Hostname of the RabbitMQ server. |
| `CASTELLUM_RABBITMQ_PORT`<br>(API only) | `5672` |  Port number to which the underlying connection is made. |
| `CASTELLUM_AUDIT_SILENT`<br>(API only) | `false` | Disable audit event logging to standard output. |
| `OS_...` | *(required)* | A full set of OpenStack auth environment variables for Castellum's service user. See [documentation for openstackclient][os-env] for details. |

All components also expect a positional argument containing the path of a YAML configuration file.
Below is a working example for a configuration file:

```yaml
max_asset_sizes:
  - asset_type: 'nfs-shares(-group:.+)?'
    value: 16384

project_seeds:
  - project_name: myproject
    domain_name: mydomain
    resources:
      nfs-shares:
        critical_threshold: { usage_percent: 95 }
        size_steps: { percent: 10 }
        size_constraints: { max_size: 8192 }
    disabled_resources:
      - 'project-quota:.*'
```

The following fields are allowed:

| Field | Type | Explanation |
| ----- | ---- | ----------- |
| `max_asset_sizes` | array of objects | If present, resource configurations for matching asset types will only be allowed if they include a compatible `max_size` constraint. If multiple constraints apply to the same resource, later constraints override earlier ones. |
| `max_asset_sizes[].asset_type` | regex | Regex that specifies which asset types this constraint applies to. |
| `max_asset_sizes[].scope_uuid` | string | If present, the constraint only applies to resources with exactly this `scope_uuid` value. This can be used to override a general constraint for a specific project or domain. |
| `max_asset_sizes[].value` | integer | Highest permissible value for the `max_size` constraint on matching resources. |
| `project_seeds` | array of objects | Specification of projects that will have resources configured. The observer will apply these seeds, and the API will reject attempts to manually override the seeded configuration. |
| `project_seeds[].project_name` | string | Name (not ID!) of the project. |
| `project_seeds[].domain_name` | string | Name (not ID!) of the domain containing the project. |
| `project_seeds[].resources.$type` | object | Specification of a resource that will be statically configured in this project. The contents of this object must be identical to the payload that will be accepted for `PUT /v1/projects/$project_id/resources/$type`. See [API spec](./docs/api-spec.md) for details. |
| `project_seeds[].disabled_resources` | list of strings | A list of regexes. Any asset type that matches one of these regexes will have autoscaling disabled and forbidden in this project. This can be used to delete resources that were configured by an earlier version of the seed. |

All regexes are matched against the entire asset type string, i.e. a leading `^` and trailing `$` are always added implicitly.

When applying project seeds, projects that do not exist in Keystone will be skipped without logging an error.

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
| `castellum_resource_scrapes`<br/>(observer) | Counter for executed resource scrape operations.<br/>Labels: `asset` (asset type), `task_outcome` (either `failure` or `success`). |
| `castellum_asset_scrapes`<br/>(observer) | Counter for executed asset scrape operations.<br/>Labels: `asset` (asset type), `task_outcome` (either `failure` or `success`). |
| `castellum_asset_resizes`<br/>(worker) | Counter for asset resize operations (see below for semantics notes).<br/>Labels: `asset` (asset type), `task_outcome` (either `failure` or `success`). |

Note that `castellum_asset_resizes{task_outcome="success"}` is incremented whenever a PendingOperation is consumed and
converted into a FinishedOperation, even if that operation moved into state "failed" or "errored". The counter
`castellum_asset_resizes{task_outcome="failure"}` is only incremented when a greenlit operation cannot be moved out of
the "greenlit" state at all. Resize operations that move into state "failed" or "errored" are counted by
`castellum_operation_state_transitions{to_state=~"failed|errored"}`.

## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/creating-an-issue). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](https://github.com/sapcc/castellum/blob/master/CONTRIBUTING.md).

## Security / Disclosure

If you find any bug that may be a security problem, please follow our instructions [in our security policy](https://github.com/SAP-cloud-infrastructure/.github/blob/main/SECURITY.md) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/SAP-cloud-infrastructure/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright 2019-2025 SAP SE or an SAP affiliate company and castellum contributors. Please see our [LICENSE](LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/sapcc/castellum).

[pq-uri]: https://www.postgresql.org/docs/9.6/static/libpq-connect.html#LIBPQ-CONNSTRING
[os-env]: https://docs.openstack.org/python-openstackclient/latest/cli/man/openstack.html
[os-pol]: https://docs.openstack.org/oslo.policy/latest/admin/policy-json-file.html
