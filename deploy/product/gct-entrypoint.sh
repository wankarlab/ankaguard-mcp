#!/bin/sh
# Entrypoint of the GoCryptoTrader container: creates /data/config.json once
# from the upstream example, with the gRPC credentials taken from the
# environment (GCT_USERNAME / GCT_PASSWORD), and starts the daemon.
# Exchange API keys are NOT configured here: the server stores credentials
# encrypted and passes them per request.
set -eu
# The daemon asks on stdin how to report NTP drift when the NTP client is on;
# a container has no stdin (EOF), so the check is disabled (level -1; 0 means
# "alert at startup", which still prompts).
# Only KuCoin is enabled: it is the one exchange the product supports, and the
# upstream example enables ~20 exchanges with their websockets.

: "${GCT_USERNAME:?GCT_USERNAME is required}"
: "${GCT_PASSWORD:?GCT_PASSWORD is required}"

CONFIG=/data/config.json
if [ ! -f "$CONFIG" ]; then
  jq --arg u "$GCT_USERNAME" --arg p "$GCT_PASSWORD" '
      .remoteControl.username = $u
    | .remoteControl.password = $p
    | .remoteControl.gRPC.enabled = true
    | .remoteControl.gRPC.listenAddress = "0.0.0.0:9052"
    | .remoteControl.gRPC.grpcProxyEnabled = false
    | .remoteControl.gRPC.grpcAllowBotShutdown = false
    | .ntpclient.enabled = -1
    | .exchanges |= map(.enabled = (.name == "Kucoin"))' \
    /opt/gct/config_example.json > "$CONFIG"
  chmod 0600 "$CONFIG"
fi

exec gocryptotrader -config "$CONFIG" -datadir /data
