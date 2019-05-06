# Overview for developers/contributors

You should have read the entire [README.md](./README.md) once before starting
to work on Castellum's codebase. This document assumes that you did that already.

## Testing methodology

### Core implementation

Run the full test suite with:

```sh
$ ./testing/with-postgres-db.sh make check
```

This will produce a coverage report at `build/cover.html`. If you don't need
that, substitute `check` for `quick-check` to get slightly better performance.

### Asset manager plugins

We do not do unit tests for the asset manager plugins. To test them under
real-life conditions, run `make test-asset-type-$ASSET_TYPE`, e.g.
`make test-asset-type-nfs-shares`. This reads all the required environment
variables from a file `.env`, which should look like this:

```sh
export CASTELLUM_DB_URI="postgres://postgres@localhost/castellum?sslmode=disable"
export CASTELLUM_ASSET_MANAGERS="nfs-shares,project-quota"
export OS_AUTH_URL="https://keystone.example.com/v3"
...
```

You will then be presented with a shell prompt that should be pretty self-explanatory.

## Random notes

- Castellum does not deal with usage values directly. It only stores
  `usage_percent = usage / size`. This is because some resources might not have
  a single usage value. For example, an instance has both RAM usage and CPU
  usage, so we would want to use the highest relative usage:

  ```
  usage_percent = max(100 * cpu_usage / num_cores, 100 * used_ram / total_ram)
  ```
