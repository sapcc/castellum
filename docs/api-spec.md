# Castellum API specification

Castellum's API looks like a conventional OpenStack REST API.

- Castellum's service URL can be found in the Keystone service catalog under the service type `castellum`.
- All endpoints require a Keystone token to be present in the `X-Auth-Token` header.
  Only Keystone v3 is supported.
- All timestamps are formatted as UNIX timestamps, i.e. seconds since the UNIX epoch.
- All percent values are floating-point numbers, although we only show integers in the examples below.

This document uses the terminology defined in the [README.md](../README.md#terminology).

* [GET /v1/projects/:id](#get-v1projectsid)
* [GET /v1/projects/:id/resources/:type](#get-v1projectsidresourcestype)
* [PUT /v1/projects/:id/resources/:type](#put-v1projectsidresourcestype)
* [DELETE /v1/projects/:id/resources/:type](#delete-v1projectsidresourcestype)
* [GET /v1/projects/:id/assets/:type](#get-v1projectsidassetstype)
* [GET /v1/projects/:id/assets/:type/:id](#get-v1projectsidassetstypeid)
* [GET /v1/projects/:id/resources/:type/operations/pending](#get-v1projectsidresourcestypeoperationspending)
* [GET /v1/projects/:id/resources/:type/operations/recently-failed](#get-v1projectsidresourcestypeoperationsrecently-failed)

## GET /v1/projects/:id

Shows information about which resources are configured for this project.
Returns 200 and a JSON response body like this:

```json
{
  "resources": {
    "nfs-shares": {
      "scraped_at": 1557134678,
      "asset_count": 42,
      "low_threshold": {
        "usage_percent": 20,
        "delay_seconds": 3600
      },
      "high_threshold": {
        "usage_percent": 80,
        "delay_seconds": 1800
      },
      "critical_threshold": {
        "usage_percent": 95
      },
      "size_constraints": {
        "minimum": 10,
        "maximum": 2000,
      },
      "size_steps": {
        "percent": 20
      }
    },
    ...
  }
}
```

The following fields may be returned:

| Field | Type | Explanation |
| ----- | ---- | ----------- |
| `resources.$type` | object | Configuration for a project resource. Resources will only be shown when a) autoscaling is enabled for them and b) the requester has sufficient permissions to read them. |
| `resources.$type.scraped_at` | timestamp | *Readonly.* When Castellum last scanned this resource for new assets or deleted assets. |
| `resources.$type.asset_count` | integer | *Readonly.* The number of assets in this resource. |
| `resources.$type.low_threshold`<br>`resources.$type.high_threshold`<br>`resources.$type.critical_threshold` | object | Configuration for thresholds that trigger an automated resize operation. Any of these may be missing if the threshold in question has not been enabled. |
| `resources.$type.low_threshold.usage_percent`<br>`resources.$type.high_threshold.usage_percent`<br>`resources.$type.critical_threshold.usage_percent` | integer | Automated operations will be triggered when usage crosses these thresholds, i.e. `usage <= threshold` for the low threshold and `usage >= threshold` for the high and critical thresholds. |
| `resources.$type.low_threshold.delay_seconds`<br>`resources.$type.high_threshold.delay_seconds` | integer | How long usage must cross the threshold before the operation is confirmed. Critical operations don't have a delay; they are always confirmed immediately. |
| `resources.$type.size_constraints.minimum`<br>`resources.$type.size_constraints.maximum` | integer | If set, resize operations will only be scheduled when the target size fits into these constraints. |
| `resources.$type.size_constraints.minimum_free` | integer | If set, downsize operations will be inhibited and upsize operations will be scheduled to ensure that `size - absoluteUsage` is always `>=` this value. |
| `resources.$type.size_steps.percent` | integer | How much the size changes in each resize operation, as a percentage of the previous size. |

## GET /v1/projects/:id/resources/:type

Shows information about an individual project resource.
Returns 404 if autoscaling is not enabled for this resource.
Otherwise returns 200 and a JSON response body like this:

```json
{
  "scraped_at": 1557134678,
  "low_threshold": {
    "usage_percent": 20,
    "delay_seconds": 3600
  },
  "high_threshold": {
    "usage_percent": 80,
    "delay_seconds": 1800
  },
  "critical_threshold": {
    "usage_percent": 95
  },
  "size_constraints": {
    "minimum": 10,
    "maximum": 2000,
  },
  "size_steps": {
    "percent": 20
  }
}
```

The response body is always identical to the `resources.$type` subobject
in the response of `GET /v1/projects/:id`. See above for explanations of all
fields.

## PUT /v1/projects/:id/resources/:type

Enables autoscaling on the specified project resource. The request body must be a JSON
document following the same schema as the response from the corresponding GET endpoint,
except that the following fields may not be present:

- `scraped_at`

Returns 202 and an empty response body on success.

## DELETE /v1/projects/:id/resources/:type

Disables autoscaling on the specified project resource. This also deletes the operation
logs for all assets in this project resource.
Returns 204 and an empty response body on success.

## GET /v1/projects/:id/assets/:type

Shows a list of all known assets in a project resource.
Returns 404 if autoscaling is not enabled for this resource.
Otherwise returns 200 and a JSON response body like this:

```json
{
  "assets": [
    {
      "id": "2535fd62-c30a-4241-8c67-a12c4fba98ad",
      "size": 100,
      "usage_percent": 42,
      "scraped_at": 1557140894,
      "stale": true
    },
    {
      "id": "acc137e0-ac0f-43c4-a3de-ba728c0091fd",
      "size": 1000,
      "usage_percent": 91,
      "scraped_at": 1557140895,
      "checked": {
        "at": 1557141495,
        "error": "cannot connect to OpenStack"
      },
      "stale": false
    },
    {
      "id": "b73894be-bcbc-44f0-b7e2-29b758c06ce9",
      "size": 20,
      "usage_percent": 60,
      "scraped_at": 1557140896,
      "stale": false
    }
  ]
}
```

For each asset, the following fields may be returned:

| Field | Type | Explanation |
| ----- | ---- | ----------- |
| `id` | string | UUID of asset. |
| `size` | integer | Size of asset. The unit depends on the asset type. See [README.md](../README.md#supported-asset-types) for more information. |
| `usage_percent` | integer | Usage of asset as percentage of size. When the asset has multiple usage types (e.g. instances have both CPU usage and RAM usage), usually the higher value is reported here. |
| `scraped_at` | integer | When the size and usage of the asset was last retrieved by Castellum. |
| `checked.at` | integer | When Castellum last tried to retrieve the size and usage of the asset. Only shown when different from `scraped_at`, i.e. when the last check failed. |
| `checked.error` | string | When the last check failed (see above), this field contains the error message that was returned from the backend. |
| `stale` | bool | This flag is set by Castellum after a resize operation to indicate that the reported size and usage are probably not accurate anymore. Will be cleared by the next scrape. |

When no scrape ever succeeded (e.g. because the asset is in an error state since creation), the fields `size`,
`usage_percent` and `scraped_at` will all be missing. The `checked.error` field will always be present in this case.

## GET /v1/projects/:id/assets/:type/:id

Shows information about a certain asset.
Returns 404 if the asset does not exist in the selected project, or if autoscaling is not
enabled for the selected project resource.
Otherwise returns 200 and a JSON response body like this:

```json
{
  "id": "acc137e0-ac0f-43c4-a3de-ba728c0091fd",
  "size": 1000,
  "usage_percent": 91,
  "scraped_at": 1557140895,
  "checked": {
    "at": 1557141495,
    "error": "cannot connect to OpenStack"
  },
  "stale": false,

  "pending_operation": {
    "state": "greenlit",
    "reason": "high",
    "old_size": 1000,
    "new_size": 1200,
    "created": {
      "at": 1557122400,
      "usage_percent": 81
    },
    "confirmed": {
      "at": 1557126000
    },
    "greenlit": {
      "at": 1557129600,
      "by_user": "980a0405-c6d2-410a-bca1-a3b109aaabc0"
    }
  },

  "finished_operations": [
    {
      "state": "cancelled",
      "reason": "high",
      "old_size": 1000,
      "new_size": 1200,
      "created": {
        "at": 1557036000,
        "usage_percent": 83
      },
      "finished": {
        "at": 1557037800
      }
    },
    ...
  ]

}
```

Most fields on the top level have the same meaning as for `GET /v1/projects/:id/assets/:type`.
The following additional fields may be returned:

| Field | Type | Explanation |
| ----- | ---- | ----------- |
| `pending_operation` | object | Information about an automated resize operation that is currently in progress. If there is no resize operation ongoing, this field will be omitted. |
| `finished_operations` | array of objects | Information about earlier automated resize operations. **This field is only shown on request** because it may be quite large. Add the query parameter `?history` to see it. |
| `finished_operations[].finished.at` | timestamp | When the operation entered its final state. |
| `finished_operations[].finished.error` | string | The backend error that caused this operation to fail. Only present when `state` is `failed.` |

The following fields may be returned for each operation, both below `pending_operation` and below `finished_operations[]`:

| Field | Type | Explanation |
| ----- | ---- | ----------- |
| `.state` | string | The current state of this operation. For pending operations, this is one of "created", "confirmed" or "greenlit". For finished operations, this is one of "cancelled", "succeeded" or "failed". See [README.md](../README.md#terminology) for details. |
| `.reason` | string | One of "low", "high" or "critical". Identifies which threshold being crossed triggered this operation. |
| `.old_size` | integer | The asset's size before this resize operation. |
| `.new_size` | integer | The (projected) asset's size after the successful completion of the resize operation. |
| `.created.at` | timestamp | When Castellum first observed the asset's usage crossing a threshold. |
| `.created.usage_percent` | string | The asset's usage at that time. |
| `.confirmed.at` | timestamp | When Castellum confirmed that usage had crossed the threshold for at least the required delay. When `reason` is `critical`, this timestamp will be identical to `.created.at`. For operations in state `created`, this field is not shown. For operations in state `cancelled`, this field may or may not be shown. |
| `.greenlit.at` | timestamp | When a user permitted this operation to go ahead. For operations not subject to operator approval, this is equal to `.greenlit.at`. For operations in states `created`, `confirmed` or `cancelled`, this field is not shown. As an exception, for operations in state `confirmed`, **this timestamp may be in the future**, which means that the operation will automatically move into state `greenlit` at that point in time. |
| `.greenlit.by_user` | string | The UUID of the user that greenlit this operation. For operations in states `created` or `confirmed`, this field is not shown. For operations not subject to operator approval, this field is not shown. |

The previous table contains a lot of rules like "this field is not shown for operations in state X". When this is confusing to you, have a look at the state machine diagram in [README.md](../README.md#terminology). The reason why many fields are optional is that they only have values when the respective state was entered in the operation's lifecycle.

## GET /v1/projects/:id/resources/:type/operations/pending

Shows information about all pending operations for assets in the given project resource.

Returns 404 if autoscaling is not enabled for this resource.
Otherwise returns 200 and a JSON response body like this:

```json
{
  "pending_operations": [
    {
      "asset_id": "acc137e0-ac0f-43c4-a3de-ba728c0091fd",
      "state": "greenlit",
      "reason": "high",
      "old_size": 1000,
      "new_size": 1200,
      "created": {
        "at": 1557122400,
        "usage_percent": 81
      },
      "confirmed": {
        "at": 1557126000
      },
      "greenlit": {
        "at": 1557129600,
        "by_user": "980a0405-c6d2-410a-bca1-a3b109aaabc0"
      }
    },
    ...
  ]
}
```

Each pending operation has the same format as the `.pending_operation` field in the JSON response returned by
`GET /v1/projects/:id/assets/:type/:id` (see above), except that the additional field `asset_id` indicates which asset
is being worked on.

For each asset in this resource, at most one pending operation will be listed.

## GET /v1/projects/:id/resources/:type/operations/recently-failed

Shows information about all operations on assets in the given project resource that **recently failed**. This is
intended to give operators a view of all assets in a resource where manual intervention may be required because
autoscaling is not working properly. Recent failure is defined as follows:

1. There is an operation in state "failed".
2. There is no newer operation for the same asset which *finished* after the failed operation.
3. The asset is still eligible for resizing for the same reason as stated in the failed operation.

Point 2 ensures that we don't see failures where the next attempt succeeded. Point 3 ensures that we don't see failures
for assets where the usage returned to normal levels in the meantime.

Returns 404 if autoscaling is not enabled for this resource.
Otherwise returns 200 and a JSON response body like this:

```json
{
  "recently_failed_operations": [
    {
      "asset_id": "acc137e0-ac0f-43c4-a3de-ba728c0091fd",
      "state": "failed",
      "reason": "high",
      "old_size": 1000,
      "new_size": 1200,
      "created": {
        "at": 1557122400,
        "usage_percent": 81
      },
      "confirmed": {
        "at": 1557126000
      },
      "greenlit": {
        "at": 1557129600,
        "by_user": "980a0405-c6d2-410a-bca1-a3b109aaabc0"
      },
      "finished": {
        "at": 1557137800,
        "error": "datacenter is on fire"
      }
    },
    ...
  ]
}
```

Each failed operation has the same format as the entries in the `.finished_operations` field in the JSON response
returned by `GET /v1/projects/:id/assets/:type/:id` (see above), except that the additional field `asset_id` indicates
which asset was being worked on.

For each asset in this resource, at most one failed operation will be listed (the most recent one).

## GET /v1/projects/:id/resources/:type/operations/recently-succeeded

Shows information about all operations on assets in the given project resource that **recently succeeded**, that is, all
operations in state "succeeded" where there is no newer operation in state "succeeded" or "failed" for the same asset.

Returns 404 if autoscaling is not enabled for this resource.
Otherwise returns 200 and a JSON response body looking like that from the `recently-failed` endpoint above, except that
`recently_failed_operations` is called `recently_succeeded_operations`.

The following query parameters can be given to filter the result:

- When `max-age` is given, only those operations will be shown that finished after `now - max_age`. The value must be an
  integer followed by one of the units `m` (minute), `h` (hour) or `d` (day), e.g. `12h` or `7d`. The default value is
  `1d`.
