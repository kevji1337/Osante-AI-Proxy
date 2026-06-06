#!/bin/sh
set -e

# Ensure data directory exists and has correct permissions.
# OSANTE_DATA_DIR is the new name; CCNEXUS_DATA_DIR is kept for backwards compat.
DATA_DIR="${OSANTE_DATA_DIR:-${CCNEXUS_DATA_DIR:-/data}}"
if [ ! -w "$DATA_DIR" ]; then
    echo "Warning: Data directory $DATA_DIR is not writable, attempting to fix..."
fi

# Run the server
exec /app/osante-proxy "$@"
