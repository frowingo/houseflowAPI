FROM valkey/valkey:8.1-alpine

COPY valkey.conf /usr/local/etc/valkey/valkey.conf

# Fly's init is PID 1, so tini has to register as a subreaper.
ENV TINI_SUBREAPER=1

# Overcommit keeps the AOF rewrite fork from failing on the small VM.
# docker-entrypoint.sh fixes /data ownership and drops to the valkey user.
CMD ["sh", "-c", "[ -n \"$VALKEY_PASSWORD\" ] || { echo 'VALKEY_PASSWORD is not set' >&2; exit 1; }; sysctl -w vm.overcommit_memory=1 >/dev/null 2>&1 || true; exec docker-entrypoint.sh valkey-server /usr/local/etc/valkey/valkey.conf --requirepass \"$VALKEY_PASSWORD\""]
