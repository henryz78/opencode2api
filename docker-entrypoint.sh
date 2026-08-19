#!/bin/sh
set -eu

app_dir=${APP_DIR:-/app}
state_dir=${STATE_DIR:-/var/lib/opencode2api}
config_path=${CONFIG_PATH:-$app_dir/config.json}
binary_path=${BINARY_PATH:-/usr/local/bin/opencode2api}
railway_port=${PORT:-}

if [ -n "${LISTEN_ADDRESS:-}" ]; then
    listen_address=$LISTEN_ADDRESS
elif [ -n "$railway_port" ]; then
    # Railway exposes one public port. Keep the API private and expose the
    # WebUI/Playground on Railway's injected PORT by default.
    api_port=8080
    if [ "$railway_port" = "8080" ]; then
        api_port=8081
    fi
    listen_address=127.0.0.1:$api_port
else
    listen_address=0.0.0.0:8080
fi

if [ -n "${WEBUI_LISTEN_ADDRESS:-}" ]; then
    webui_listen_address=$WEBUI_LISTEN_ADDRESS
elif [ -n "$railway_port" ]; then
    webui_listen_address=0.0.0.0:$railway_port
else
    webui_listen_address=0.0.0.0:8081
fi

mkdir -p "$state_dir"

if [ -f "$config_path" ]; then
    active_config=$config_path
else
    generated_config=$state_dir/config.json
    if [ ! -f "$generated_config" ]; then
        if [ ! -f "$app_dir/config.example.json" ]; then
            printf '%s\n' "config template not found at $app_dir/config.example.json" >&2
            exit 1
        fi
        cp "$app_dir/config.example.json" "$generated_config"
    fi
    active_config=$generated_config
    printf '%s\n' "config.json not found; using a persistent generated copy. Set real API keys or enable anonymous mode, and change the WebUI password before use."
fi

if [ ! -x "$binary_path" ]; then
    printf '%s\n' "opencode2api binary not found at $binary_path" >&2
    exit 1
fi

exec "$binary_path" -config "$active_config" -listen "$listen_address" -web-listen "$webui_listen_address"
