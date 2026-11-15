#!/bin/sh
# Lets mergelanes create its virtual adapter without running as root. File capabilities are
# dropped whenever the binary is replaced, so this runs on every install and upgrade.
if command -v setcap >/dev/null 2>&1; then
    setcap cap_net_admin,cap_net_raw+ep /usr/bin/mergelanes || echo "mergelanes: setcap failed; run: sudo setcap cap_net_admin,cap_net_raw+ep /usr/bin/mergelanes"
else
    echo "mergelanes: setcap not found; install libcap and run: sudo setcap cap_net_admin,cap_net_raw+ep /usr/bin/mergelanes"
fi

exit 0
