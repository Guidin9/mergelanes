# Security

## Reporting a problem

Please **do not open a public issue** for a security problem.

Report it privately instead:

- Use **Report a vulnerability** on the repository's [Security tab](https://github.com/Guidin9/mergelanes/security/advisories/new).

Please include what you found, the steps to reproduce it, and your OS and `mergelanes version`. I will
answer as soon as I can and credit you in the fix if you want.

## Supported versions

Only the [latest release](https://github.com/Guidin9/mergelanes/releases/latest) gets fixes.

## What matters most

MergeLanes runs with administrator or root rights, so these are the parts to look at:

- **macOS:** `sudo mergelanes install` copies the binary to `/usr/local/bin` and adds
  `/etc/sudoers.d/mergelanes`, which lets your user run exactly `mergelanes tray` and `mergelanes cleanup` as
  root without a password. Its state lives in the root-owned `/var/run/mergelanes`. Ways to turn that into
  more than it should allow are in scope.
- **Linux:** the binary is given `CAP_NET_ADMIN` and `CAP_NET_RAW` with `setcap`.
- **Windows:** the installer and the elevated tray. The tray keeps everything it writes, deletes or reads
  back in `%ProgramData%\MergeLanes`, which only SYSTEM and Administrators can change; users may only add
  request files to its `ctl\` folder, which the tray opens without following links. It runs `netsh`,
  `route`, `schtasks` and PowerShell by their full paths in System32, and *Start at sign-in* (a task that
  starts the tray elevated with no prompt) is only allowed for the copy installed in Program Files.
- **Per-app lanes (Windows):** the tray reads Windows' TCP and UDP tables to find which program owns a
  connection. A program can only influence which adapter its own connections use.
- Anything that lets a normal user run code as root or admin, read or write files through the root
  process, or leave the machine without a working network after a crash.

MergeLanes is not a VPN. Your traffic goes straight out of each adapter and is not encrypted by MergeLanes.
That is by design and not a vulnerability.
