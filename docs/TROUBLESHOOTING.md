# Troubleshooting

Start here when the install or the setup page does not work. Find the message or symptom, then do
what it says. If nothing helps, see [Still stuck](#still-stuck) at the end.

Where things are, if you used the installer:

| | Windows | Linux |
|---|---|---|
| Program folder | `%LOCALAPPDATA%\jellyfin-telegram` | `~/jellyfin-telegram` |
| Settings | `.env` in that folder | `.env` in that folder |
| Log (send the last lines) | `data\jellyfin-telegram.log` | `data/jellyfin-telegram.log` |
| Data, sessions, Jellyfin | `data\` in that folder | `data/` in that folder |
| Panel | `http://127.0.0.1:8097/panel` | `http://127.0.0.1:8097/panel` |

## Install problems

**Windows says "Windows protected your PC" (SmartScreen).**
The program is new and not signed, so Windows warns. Click **More info**, then **Run anyway**.

**PowerShell says "running scripts is disabled".**
Use the `install-windows.cmd` file instead (double-click it). It runs the installer with the
right setting. Or, in a PowerShell window:
`irm https://github.com/Aryan-mor/jellyfin-telegram/releases/latest/download/install.ps1 -OutFile $env:TEMP\jellyfin-telegram-install.ps1; & $env:TEMP\jellyfin-telegram-install.ps1`

**The installer window says only "Access is denied."**
Defender blocks the old `irm | iex` command line (`Trojan:Win32/Commando.B!ml`). Download a fresh
`install-windows.cmd` or `install-nightly-windows.cmd` from the latest release. Do not turn Defender off.

**The download fails or says 404.**
Check that you are online and that the [Releases page](https://github.com/Aryan-mor/jellyfin-telegram/releases)
has at least one release. If a proxy or antivirus blocks GitHub, download the zip or tar.gz for
your system by hand, unpack it into an empty folder and run the one file inside.

**My antivirus deleted the file.**
Unsigned new programs are sometimes flagged by mistake. Restore it, add the program folder to the
exclusions, and run the installer again. You can compare the file with `SHA256SUMS.txt` on the release.

**Linux: "Unsupported CPU".**
Builds exist for x86-64 (`amd64`) and ARM64 (`arm64`) only.

**Linux: no address is printed, or the service does not start.**
Run `journalctl --user -u jellyfin-telegram -n 50` and read the last lines. If systemd is not
available for your user, run `~/jellyfin-telegram/jellyfin-telegram` in a terminal instead. It
prints the setup address.

## The setup page does not open

- Open the address by hand. In a terminal, go to the program folder and run the program; it prints
  `http://127.0.0.1:8097/setup?code=...`. The code changes on every start, so use the newest one.
- **"cannot serve on ...: address already in use".** Another program uses port 8097, or
  jellyfin-telegram is already running (check the Task Manager or `ps`). Stop the other one, or
  put `GATEWAY_LISTEN=0.0.0.0:8100` in `.env` to use another port.
- **The page is blank or says 403.** Open `/setup` from the same computer that runs the program,
  and use the full address with `?code=`. Setup is refused from other machines on purpose.
- **A firewall question appears.** Allow it on private networks. It is only needed to open
  Jellyfin from your phone or TV.

## Signing in with your phone number

**"this build has no built-in api_id. Open Advanced and enter yours".**
This build was made without the shared Telegram app. Open **Advanced** on the setup page and enter
your own `api_id` and `api_hash`:

1. Go to [my.telegram.org](https://my.telegram.org) and sign in with your phone number.
2. Open **API development tools** and create an app (any name).
3. Copy the `api_id` (a number) and `api_hash` (letters and digits) into the setup page.

If my.telegram.org shows only "ERROR", wait a few minutes and try again, try another network or
turn off a VPN. This is a Telegram-side problem that is usually temporary.

**The code does not arrive.**
It comes as a message **inside the Telegram app** (from "Telegram"), not as an SMS. Check your
other devices. Write the number with the country code, for example `+49...`. After several
attempts Telegram makes you wait; wait the time it says and try once.

**"Too many attempts" or "flood".**
Telegram limits how often you can ask for a code or create bots. Wait, without retrying, for
the time shown (it can be an hour or a day), then try again.

**It asks for a password.**
That is your Telegram two-step verification password, not the code.

**"Telegram allows 20 bots on one account, and this one is full".**
Delete bots you do not use in [@BotFather](https://t.me/BotFather) (`/mybots`), or give the
setup page a link to one message in a channel whose bots are already admins.

**"@BotFather did not answer" or "did not give a token".**
Try again in a minute. If it keeps failing, create the bots by hand with `/newbot` in BotFather and
use the **Manual** option on the setup page.

**"this Telegram account already has as many public channels as Telegram allows".**
Make one of your public channels private, or keep the new channel private.

## After the sign-in

**"Jellyfin did not start in time".**
The first start downloads and prepares Jellyfin, which can take a few minutes on a slow disk.
Wait, then restart the program. If it fails again, read the Jellyfin log in `data/jellyfin/`.
Make sure the disk has at least 2 GB free.

**"no Jellyfin build for ...".**
There is no automatic Jellyfin for your system. Install Jellyfin yourself, then use
the existing-Jellyfin option in setup (it needs the address and an API key), or set `JELLYFIN_URL`.

**"checksum mismatch, refusing to run it".**
The Jellyfin download was damaged on the way. Run again; if it repeats, your network or proxy is
changing the file.

**A bot is marked red in the panel.**
Open the channel in Telegram, go to *Administrators* and make sure the bot is an admin that can
post messages.

**I uploaded a video and nothing shows up.**
Wait a minute: the library is updated about once a minute. Then check that the file is a video
(not a photo) and that Jellyfin has finished its first scan.

**Playback stutters or does not start.**
More bots give more speed. Add bots in the panel under **Bots**. A slow home connection is also
a common cause.

## Start again from zero

This removes your settings and sign-in, not the videos in your Telegram channel.

1. Stop the program (Windows: end `jellyfin-telegram.exe` in Task Manager; Linux:
   `systemctl --user stop jellyfin-telegram`).
2. In the program folder, delete `.env` and the `data` folder (and `library` if you want a fresh
   library).
3. Start the program. The setup page comes back.

## Update or remove

- **Update:** run the install command from the README again. Settings and library are kept.
- **Remove on Windows:** delete the program folder, the `jellyfin-telegram` task in Task Scheduler,
  and the `jellyfin-telegram panel` shortcuts on the Desktop and in the Start menu.
- **Remove on Linux:** `systemctl --user disable --now jellyfin-telegram`, then delete
  `~/jellyfin-telegram`, `~/.config/systemd/user/jellyfin-telegram.service` and the
  `jellyfin-telegram.desktop` files.

## Still stuck

Open an [issue](https://github.com/Aryan-mor/jellyfin-telegram/issues) and include:

- your system (Windows or Linux, and the version) and the program version shown in the panel,
- the exact message you saw, and what you clicked just before,
- the last lines of the log, `data/jellyfin-telegram.log` in the program folder.

Hide your bot tokens, `api_hash`, the setup `code` and any phone numbers before you post.
