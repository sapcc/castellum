# Overview for developers/contributors

TODO write more

## Running the test suite

Run the full test suite with:

```sh
$ ./testing/with-postgres-db.sh make check
```

This will produce a coverage report at `build/cover.html`. If you don't need
that, substitute `check` for `quick-check` to get slightly better performance.

## Core terminology and state machine

The database schema contains the following basic objects:

- An **asset** is a thing that Castellum can resize, e.g. an instance or an NFS
  share. It has a **usage** and a **size**, such that `0 <= usage <= size`.
  Castellum does not deal with the usage directly, it only stores
  `usage_percent = usage / size`. This is because some resources might not have
  a single usage value. For example, an instance has both RAM usage and CPU
  usage, so we use the highest relative usage:

  ```
  usage_percent = max(100 * cpu_usage / num_cores, 100 * used_ram / total_ram)
  ```

- A **resource** is the sum of all assets of a certain type within a certain
  scope, for instance, "NFS shares in project X" or "loadbalancer project
  quotas in domain Y".

- An **operation** is when Castellum resizes (or wants to resize) an asset.
  Operations go through the following state transitions:

  - `() -> created`: An operation is created whenever an asset's usage crosses
    one of the thresholds defined on the resource.

  - `created -> confirmed`: Castellum sets the operation to "confirmed" when
    the usage has been over the threshold for the delay configured on the
    resource. For the "critical" threshold, there is no such delay, so those
    operations go into "confirmed" immediately.

  - `created -> cancelled`: If usage drops back to normal levels before the
    operation is "confirmed", it is "cancelled" instead.

  - `confirmed -> greenlit`: If the resource is configured such that operations
    require approval from an operator, operations stay in "confirmed" until
    they are explicitly greenlit by an authorized user. If no approval is
    required, operations go from "confirmed" into "greenlit" immediately.

  - `greenlit -> succeeded/failed`: Once greenlit, one of Castellum's workers
    will execute the resizing operation and set the state to either "succeeded"
    or "failed" accordingly.

