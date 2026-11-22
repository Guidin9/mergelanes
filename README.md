<p align="center">
  <img src="docs/icon.png" width="112" alt="MergeLanes icon">
</p>

<h1 align="center">MergeLanes</h1>

<p align="center">
  <strong>Use all your internet connections at the same time, with a lane for each app.</strong><br>
  Downloads get the speed of Wi-Fi, Ethernet and your phone added up. Games and voice chat stay on the
  connection with the lowest ping.
</p>

<p align="center">
  <a href="https://github.com/Guidin9/mergelanes/releases/latest">Download</a> ·
  <a href="#install">Install</a> ·
  <a href="#apps-and-lanes">Apps and lanes</a> ·
  <a href="#add-a-second-connection">Add a second connection</a> ·
  <a href="#troubleshooting">Troubleshooting</a> ·
  <a href="docs/DEVELOPING.md">How it works</a>
</p>

<p align="center">
  <a href="https://github.com/Guidin9/mergelanes/actions/workflows/ci.yml"><img src="https://github.com/Guidin9/mergelanes/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/Guidin9/mergelanes/releases"><img src="https://img.shields.io/github/v/release/Guidin9/mergelanes" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-GPL--3.0--or--later-blue" alt="GPL-3.0-or-later"></a>
</p>

MergeLanes is a fork of [net-merge](https://github.com/net-merge/net-merge) by Rohit Mahto. It keeps
everything net-merge does and adds **lanes**: it knows which app opened each connection, so you can decide
per app which connection it uses. It works on **Windows, macOS and Linux** (per-app rules on Windows for
now). There is no account, no server and no VPN.

## How it helps

MergeLanes gives every new connection to one of your links. When an app opens many connections, they spread
over all of them.

- **Faster:** Steam, Epic and Riot downloads, browsers, system updates, cloud uploads, download managers,
  torrents.
- **Not faster:** one single connection. One connection always uses one link, so one big file over one
  connection does not get faster (see the browser tip under [Use it](#use-it)).
- **Steady games:** with no setup at all, games and voice chat stay on the adapter with the lowest ping, so
  merging never moves them to the slower link.
- **Works for every app**, TCP and UDP, IPv4 and IPv6. No proxy settings.
- **Safe:** if MergeLanes crashes, your internet keeps working.

A real test on a dormitory network that limits each device to a few Mbit/s: Ethernet alone gave
5.5 Mbit/s and a second Wi-Fi alone 7.5 Mbit/s. Through MergeLanes both carried traffic at the same time:
**5.6 + 7.5 = 13.1 Mbit/s**.

## Install

**Windows:** download [mergelanes-setup.exe](https://github.com/Guidin9/mergelanes/releases/latest/download/mergelanes-setup.exe)
and run it. Windows may warn because the app is not code-signed yet: click *More info*, then *Run anyway*.
There is also a [portable zip](https://github.com/Guidin9/mergelanes/releases/latest/download/mergelanes-portable.zip)
(it cannot start at sign-in, see [Security](#security)).

**Linux:** download the package or tarball for your CPU from the
[latest release](https://github.com/Guidin9/mergelanes/releases/latest). The `.deb` / `.rpm` / `.apk` /
Arch packages give MergeLanes the network rights it needs. With the tarball:

```
curl -fsSL https://github.com/Guidin9/mergelanes/releases/latest/download/mergelanes-linux-$(uname -m).tar.gz | tar -xz -C /tmp mergelanes && sudo install -m 755 /tmp/mergelanes /usr/local/bin/ && sudo setcap cap_net_admin,cap_net_raw+ep /usr/local/bin/mergelanes
```

**macOS:**

```
curl -fsSL https://github.com/Guidin9/mergelanes/releases/latest/download/mergelanes-macos-universal.tar.gz | tar -xz -C /tmp mergelanes && sudo /tmp/mergelanes install && mergelanes tray
```

`sudo mergelanes install` copies it to `/usr/local/bin` and adds one rule so that only `mergelanes tray`
and `mergelanes cleanup` can run as root without a password. On macOS run `xattr -d com.apple.quarantine
mergelanes` if it says it cannot open it.

Then start it from the Start menu (Windows) or with `mergelanes tray`.

## Apps and lanes

Open the tray menu and go to **Apps**. It lists the apps that connected in the last ten minutes (apps with
a rule always stay). Pick a lane for any of them:

| Lane | What it does | Good for |
|---|---|---|
| **Auto** (default) | Games and voice chat on the lowest-ping adapter, everything else merged | Almost everything |
| **Merge all connections** | Every connection spread over all adapters, UDP too | Torrent clients, download managers |
| **Lowest ping** | Every connection on the adapter with the lowest ping | A game that also uses TCP, remote desktop |
| **Only Ethernet** (any adapter) | Every connection on that adapter; the fastest other one if it goes down | Keeping an app off your phone's mobile data |

- **Auto needs no setup.** It treats UDP to anything but web (QUIC on port 443), DNS and local discovery as
  realtime, which is what games and voice chat send. Torrent clients and download managers are merged even
  in Auto, because their UDP is bulk data.
- **Lowest ping is measured,** every 15 seconds per adapter. The menu shows each adapter's ping and which
  one games use. Games stay on it until another adapter is clearly faster (25% or more), so they do not hop.
- **A new lane applies to new connections.** Open ones stay where they are; restart the app, or wait for
  it to reconnect.

From a terminal:

```
mergelanes apps                                   apps seen recently, their lane and where they went
mergelanes app steam.exe merge                    Steam: every download over all adapters
mergelanes app "VALORANT-Win64-Shipping.exe" fastest
mergelanes app chrome.exe Ethernet                Chrome only on Ethernet (falls back if it fails)
mergelanes app chrome.exe auto                    back to Auto
```

App names are the program's file name and are matched without case (`Steam.exe`, `steam.exe` and `steam`
are the same app). On macOS and Linux MergeLanes cannot see which app owns a connection yet, so every app
follows Auto there.

### Chat

Instead of the menus, say what you want: click the tray icon (right-click it for the menu), pick
**Chat…** in the menu, or click the MergeLanes shortcut while it is already running, and type, in any
language:

- *"Spotify on Wi-Fi, Brave on Ethernet"*: sets both lanes.
- *"Which connection does Brave use?"*: its lane and how its connections were split over the adapters.
- *"Which mode should Discord use?"*: a recommendation with the reason. Nothing changes until you say so.
- *"How are my connections doing?"*, *"Limit Wi-Fi to 5 Mbit/s"*, *"Pause merging"*.

The chat runs on Google's Gemini API with your own API key; a free one from
[Google AI Studio](https://aistudio.google.com/apikey) is enough. Paste it in the chat's settings, or run
`mergelanes ask -set-key` and paste it. MergeLanes tries the fast Flash-Lite models first and moves on to
the next one by itself when a model is busy, out of free quota or retired by Google, so there is nothing to
pick or update. `mergelanes ask "..."` is the same chat in a terminal, on every OS.

On the free tier, what you type and the names of your apps and connections go to Google, which may use them
to improve its products. The content of your traffic is never sent. The window is Windows-only for now (it
uses the WebView2 Runtime that ships with Windows 11).

## Add a second connection

MergeLanes needs **two or more** connections that reach the internet on their own path. Use Wi-Fi plus a
phone (USB tethering or hotspot), an Ethernet cable, or a second Wi-Fi. Plug it in and MergeLanes adds it
within a few seconds. When you unplug it, its connections move to the others.

- Wi-Fi and Ethernet from the **same home router** share one internet line, so the total stays the same.
  On networks that limit each device (dormitories, hotels, some offices), two connections do add up.
- **Windows and Linux:** plug the phone in and turn on *USB tethering* (Android) or *Personal Hotspot*
  (iPhone).
- **Mac with an iPhone:** plug it in, tap *Trust*, and turn on *Settings > Personal Hotspot > Allow Others
  to Join*. **Mac with an Android phone** needs the app TetherKit; start MergeLanes first, then connect the
  phone.
- A phone uses its mobile data, so part of your traffic goes through it. Pin apps to your other adapter
  (*Only Wi-Fi*) or quit MergeLanes when you do not want that.

## Use it

1. Start MergeLanes. The tray icon is **green** while it merges and **grey** while paused.
2. It **pauses by itself** when fewer than two connections have internet, and starts again when a second
   one works. Pausing or starting it yourself always wins. Untick *Pause when there is only one connection*
   to switch that off.
3. **Hover** the icon for each connection's speed. **Right-click** it (on Mac, click) for the menu: each
   connection's speed, ping and flows, *Apps*, *Pause merging* and *Quit*. Click a connection to cap its
   speed (1 to 100 Mbit/s); `mergelanes limit "Ethernet 2" 25` sets any other cap.
4. **Start at sign-in:** tick it in the menu (or the box in the Windows installer).

Tips:
- **Faster browser downloads.** A browser downloads a file over one connection. Chromium browsers can split
  it: open `chrome://flags/#enable-parallel-downloading` (Edge: `edge://`, Brave: `brave://`), set it to
  *Enabled* and relaunch. Or use a download manager, for example `aria2c -x 8 -s 8 <link>`.
- Torrent clients already use every connection; leave their network interface on *Any*.
- `mergelanes list` shows which connections are used and why others are skipped.

## Commands

```
mergelanes tray            tray icon (the normal way to run it)
mergelanes on | off        start / pause merging in the running tray
mergelanes quit            pause and close the tray
mergelanes status | list   live numbers / which connections are used
mergelanes apps            apps seen recently and their lanes
mergelanes app <app> auto|merge|fastest|<adapter>
mergelanes chat            Windows: the chat window
mergelanes ask "..."       the chat in a terminal; ask -set-key saves a Gemini API key
mergelanes limit <adapter> <Mbit/s>
mergelanes autostart on|off|status
mergelanes socks           plain SOCKS5 proxy on 127.0.0.1:1080 (no admin, per app)
mergelanes cleanup         remove leftover routes after a crash
mergelanes install         macOS only: sudo mergelanes install
```

More flags and details: [docs/DEVELOPING.md](docs/DEVELOPING.md).

## Troubleshooting

<details>
<summary><strong>A second connection does not make things faster</strong></summary>
<br>

Each connection must reach the internet by its own path, or the network must limit each device. Two
connections to the same home router share one line. Also, one connection always uses one link: run
`mergelanes status` while downloading to see how many connections each link carries.
</details>

<details>
<summary><strong>An app is not in the Apps menu</strong></summary>
<br>

It appears once it opens a connection while MergeLanes is merging, and drops off after ten minutes without
new connections unless it has a rule. Some games run as a separate program from their launcher (for
example `VALORANT-Win64-Shipping.exe` next to `RiotClientServices.exe`): pick the one that shows up while
you play. On macOS and Linux the menu stays empty, see [Apps and lanes](#apps-and-lanes).
</details>

<details>
<summary><strong>It does nothing while a VPN is on</strong></summary>
<br>

A VPN app on your computer (Cloudflare WARP, ProtonVPN and others) sends all your internet through itself,
so there is nothing to share. Turn the VPN app off, or set the VPN up on your router instead.
</details>

<details>
<summary><strong>Windows uses only one network</strong></summary>
<br>

Windows can switch Wi-Fi off when Ethernet is plugged in. In the Group Policy Editor (`gpedit.msc`) go to
*Computer Configuration > Administrative Templates > Network > Windows Connection Manager* and set
**Minimize the number of simultaneous connections to the Internet or a Windows domain** to **Disabled**.
</details>

<details>
<summary><strong>A site asks me to log in again</strong></summary>
<br>

Your connections may have different public IPs, and some sites tie a login to one IP. Pin that browser to
one adapter (*Only Ethernet*) and it keeps one IP.
</details>

<details>
<summary><strong>"Paused: one connection"</strong></summary>
<br>

MergeLanes only has something to do with two or more connections that have internet, so it waits. Connect
the second one and it starts by itself after a few seconds, or choose *Start merging*.
</details>

## Security

MergeLanes needs admin rights on Windows (`CAP_NET_ADMIN` on Linux, `sudo mergelanes install` once on
macOS). On Windows the elevated tray keeps everything it writes in `%ProgramData%\MergeLanes`, which only
administrators can change, runs Windows tools by their full path, and only lets the copy installed in
Program Files start at sign-in. The chat never runs elevated: it is a separate process of the signed-in
user, which changes settings through the same folder as the `mergelanes app` command, and keeps the Gemini
API key in that user's profile, encrypted with Windows DPAPI. MergeLanes is not a VPN: traffic leaves each
adapter as it normally would, unencrypted by MergeLanes. Found a problem? See [SECURITY.md](SECURITY.md).

## Uninstall

**Windows:** *Settings > Apps > Installed apps > MergeLanes > Uninstall*. **Linux and macOS:** run
`mergelanes quit; mergelanes autostart off`, then remove the package or `/usr/local/bin/mergelanes` (on macOS
`sudo mergelanes uninstall` first). Your network goes back to normal as soon as MergeLanes stops.

## Credits and license

MergeLanes is a fork of [net-merge](https://github.com/net-merge/net-merge) 0.3.7 by Rohit Mahto, who wrote
the merging engine, the tray and the platform code it is built on.

[GPL-3.0-or-later](LICENSE) © 2026 Mert Demirdöven. Code from net-merge stays under its MIT license
© 2026 Rohit Mahto, see [LICENSE-net-merge](LICENSE-net-merge). Third-party parts:
[THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).
