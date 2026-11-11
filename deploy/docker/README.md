# Echo Service Docker

Copy `.env.example` to `.env`, set `ECHO_DATA_DIR` to the host directory that
already contains (or will contain) `echo-service.db`, then run:

```sh
mkdir -p data
docker compose --env-file .env up -d --build
```

For an exported image, run `docker load -i echo-service-image.tar.gz` first,
then use `docker compose --env-file .env up -d --no-build`.
The exported image must match the deployment host's CPU architecture;
check with `docker image inspect echo-service:amd64 --format '{{.Architecture}}'`.

The source package includes vendored Go dependencies, so image builds do not
need access to the private Go module server.

The SQLite database is `/app/data/echo-service.db` inside the container and is
persisted in the host directory configured by `ECHO_DATA_DIR`. Mount the whole
database directory, including SQLite sidecar files, rather than mounting only
the `.db` file. The directory must be writable by container UID `10001`; for
example, `chown -R 10001:10001 /path/to/echo-data`. Keep a backup before moving
an existing database. The image listens on `0.0.0.0:9090` and is intended to sit
behind the Web gateway or another authenticated reverse proxy.

Authentication defaults in `.env`:

```dotenv
ECHO_AUTH_SINGLESESSION=false
ECHO_AUTH_ACCESSTOKENTTL=24h
ECHO_AUTH_REFRESHTOKENTTL=168h
ECHO_AGENTCONNECTION_MAXMESSAGEBYTES=52428800
```

`ECHO_AGENTCONNECTION_MAXMESSAGEBYTES` is the maximum size of one ACP/WebSocket
message in bytes. The default `52428800` is 50 MiB. It is not a Session history
cache size; it applies to each individual Agent event and the corresponding Web
event broadcast.

Independent logins coexist by default. Set `ECHO_AUTH_SINGLESESSION=true` to
revoke older logins on successful login. Refresh and single-device logout affect
only that login's tokens and event sockets. Logout-all and account/assignment
revocation still invalidate the relevant account connections. Existing tokens
retain their original expiry; new/rotated access tokens use the configured TTL,
capped by the login session's absolute refresh expiry.
