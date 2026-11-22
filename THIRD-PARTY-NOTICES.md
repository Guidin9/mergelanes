# Third-party notices

MergeLanes is licensed GPL-3.0-or-later (see `LICENSE`). It is a fork of
[net-merge](https://github.com/net-merge/net-merge) by Rohit Mahto, whose code stays under the MIT license
(see `LICENSE-net-merge`). It links the following libraries, all under licenses compatible with the GPL
(license texts are in each project's repository and in the Go module cache):

| Component | License | Use |
|---|---|---|
| [tun2socks](https://github.com/xjasonlyu/tun2socks) | MIT | TUN device + userland TCP/IP stack glue |
| [gVisor](https://github.com/google/gvisor) (netstack) | Apache-2.0 | userland TCP/IP stack |
| [wireguard-go](https://git.zx2c4.com/wireguard-go) / [wintun Go bindings](https://git.zx2c4.com/wintun-go) | MIT | Windows TUN device access |
| [fyne.io/systray](https://github.com/fyne-io/systray) | Apache-2.0 | system tray icon |
| [vishvananda/netlink](https://github.com/vishvananda/netlink) | Apache-2.0 | Linux TUN addresses and routes, default-route discovery |
| [jchv/go-webview2](https://github.com/jchv/go-webview2) | MIT | Windows chat window (WebView2) |
| [jchv/go-winloader](https://github.com/jchv/go-winloader) | ISC | loads the WebView2 loader for go-webview2 |
| golang.org/x/sys, golang.org/x/net, golang.org/x/time, golang.org/x/crypto | BSD-3-Clause | Windows and Linux system APIs, support code |
| Other transitive modules (see `go.mod` / `go.sum`) | MIT / Apache-2.0 / BSD | |

## Wintun

The installer and portable zip include `wintun.dll`, the prebuilt, signed driver from
<https://www.wintun.net/>, unmodified, under the Prebuilt Binaries License. Copyright (C) 2018-2021
WireGuard LLC. The license text is shipped next to it as `wintun-LICENSE.txt`. The DLL is not stored
in this repository: `scripts/fetch-wintun.ps1` downloads it and verifies its SHA-256 and its
WireGuard LLC code signature.
