# Castellum API specification

Castellum's API looks like a conventional OpenStack REST API.

- All endpoints require a Keystone token to be present in the `X-Auth-Token` header.
  Only Keystone v3 is supported.

- All timestamps are formatted as UNIX timestamps, i.e. seconds since the UNIX epoch.

# GET /v1/projects/:id

Shows information about which resources are configured for this project.
Returns 200 and a JSON response body like this:

```json
{
  "resources": {
    "nfs-shares": {
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
| `resources.$type.scraped_at` | timestamp | When Castellum last scanned this resource for new assets or deleted assets. |
| `resources.$type.low_threshold`<br>`resources.$type.high_threshold`<br>`resources.$type.critical_threshold` | object | Configuration for thresholds that trigger an automated resize operation. Any of these may be missing if the threshold in question has not been enabled. |
| `resources.$type.low_threshold.usage_percent`<br>`resources.$type.high_threshold.usage_percent`<br>`resources.$type.critical_threshold.usage_percent` | integer | Automated operations will be triggered when usage crosses these thresholds, i.e. `usage <= threshold` for the low threshold and `usage >= threshold` for the high and critical thresholds. |
| `resources.$type.low_threshold.delay_seconds`<br>`resources.$type.high_threshold.delay_seconds` | integer | How long usage must cross the threshold before the operation is confirmed. Critical operations don't have a delay; they are always confirmed immediately. |
| `resources.$type.size_steps.percent` | integer | How much the size changes in each resize operation, as a percentage of the previous size. |

# GET /v1/projects/:id/resources/:type

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
  "size_steps": {
    "percent": 20
  }
}
```

The response body is always identical to the `resources.$type` subobject
in the response of `GET /v1/projects/:id`. See above for explanations of all
fields.

# PUT /v1/projects/:id/resources/:type

Enables autoscaling on the specified project resource. The request body must be a JSON
document following the same schema as the response from the corresponding GET endpoint,
except that the following fields may not be present:

- `scraped_at`

Returns 202 and an empty response body on success.

# DELETE /v1/projects/:id/resources/:type

Disables autoscaling on the specified project resource. This also deletes the operation
logs for all assets in this project resource.
Returns 204 and an empty response body on success.

# GET /v1/projects/:id/assets/:type
# GET /v1/projects/:id/assets/:type/:id
