# Changelog

What changed in each release. Each version's section becomes its GitHub release notes, so add the
entry here before pushing a version tag.

## [Unreleased]

### Added
- **Chat (Windows window, and `mergelanes ask` everywhere).** Set lanes, speed limits and merging in plain
  language, ask which connection an app uses or which lane suits it. It uses Google's Gemini API with your
  own (free) key, tries several models and moves on when one is busy, out of quota or retired. On Windows
  a left click on the tray icon opens it (the right click shows the menu, which also has *Chat…*), and so
  does the MergeLanes shortcut while MergeLanes is running. It runs as the signed-in user, never
  elevated, and keeps the key encrypted with DPAPI in that user's profile.
- **Where each app's connections went.** `status.json` now counts each app's connections per adapter under
  its current rule, which is how the chat answers "which connection does Brave use?" for merged apps.

### Fixed
- **Parallel downloads are split over the adapters.** A downloader that opens several connections to one
  server at once (Epic, Steam, browsers) had them all placed on whichever adapter looked least busy at that
  moment, often because chatty but tiny flows counted as busy on the other one, so the download ran on one
  link. Connections to the same host and port are now spread over the adapters by their weight, and a link
  whose speed is not known yet (right after a start) gets an equal share until it shows otherwise; idle
  keep-alive sockets to the same address do not count. Measured: an Epic download went from about 7.5 to
  11-14 Mbit/s on two links of about 6-7.5 each.

## [0.1.1] - 2026-11-15

### Fixed
- **Windows: apps are recognised in a burst of new connections too.** When an app opened a connection
  within 15 ms of the last look at Windows' connection table, MergeLanes could skip looking again and treat
  it as an unknown app, so its rule did not apply to that connection. Windows' clock moves in 15 ms steps
  on an idle PC; the lookups are now counted instead of timed.
- **Windows: ping is measured to the microsecond.** For the same reason, a link with less than 15 ms of
  latency could be measured as 0 (unmeasured) or 15 ms, so *Lowest ping* could pick the slower link. The
  probe now times its handshake with Windows' performance counter.

## [0.1.0] - 2026-11-15

The first MergeLanes release. It starts from [net-merge](https://github.com/net-merge/net-merge) 0.3.7 by
Rohit Mahto, so everything net-merge does is here: per-connection merging over Wi-Fi, Ethernet and phone
tethers, internet probes, speed limits per adapter, IPv6 handling and the tray. What is new:

### Added
- **A lane for each app (Windows).** MergeLanes finds which program opened each connection and lets every
  app have its own lane. Pick it in the tray under *Apps*, which lists the apps that connected in the last
  ten minutes, or with `mergelanes app <app> <mode>`:
  - *Auto* (the default): games and voice chat stay on the connection with the lowest ping, everything else
    is merged. No setup needed.
  - *Merge all connections*: every connection of the app is spread over all adapters, UDP too.
  - *Lowest ping*: every connection of the app on the lowest-ping adapter.
  - *Only Ethernet* (or any adapter): every connection on that adapter, and on the fastest other one if it
    goes down.
- **Latency per adapter.** The internet probe now times its handshake. The tray shows each adapter's ping
  and which one games use; games stay there until another adapter is clearly faster, so they do not hop
  between links.
- `mergelanes apps` lists the apps seen recently, their rule and where their last connection went.

### Changed
- **Windows: the elevated tray only writes to `%ProgramData%\MergeLanes`.** Its status, log, limits and app
  rules moved there from `%LOCALAPPDATA%`, and the sign-in task's XML and the weak host settings to restore
  moved there from `%TEMP%`. Only administrators can change that folder, so another program running as you
  can no longer plant a link or swap a file there to make the elevated tray overwrite or delete files, or
  register its own sign-in task. Saved weak host settings are checked before they go to `netsh`, request
  files are opened without following links, and `netsh`, `route`, `schtasks` and PowerShell run by their
  full path.
- **Windows: *Start at sign-in* needs the installed copy.** A portable copy in a folder you can write to
  would let any program that replaces it run as admin at every sign-in.
- Licensed GPL-3.0-or-later. net-merge's code keeps its MIT license, see `LICENSE-net-merge`.
- The apt, dnf, zypper, Alpine, Arch, Homebrew and Nix repositories of net-merge are not carried over. The
  release has the Windows installer and portable zip, Linux tarballs and packages, and a macOS tarball.
