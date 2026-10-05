# 8. Turning KOReader's SSH server off from ember

A Kindle whose SSH server stays on after the user is done accepts logins from the
whole network (and, with "Login without password" left on, from anyone). Turning
it off means walking to the Kindle and opening KOReader's menu. ember's Disconnect
offers to do it from the dashboard instead, and recommends it.

## What it does

Disconnect first releases keep-awake (ADR-0007), so the Device can sleep again.
With "turn off the SSH server" chosen, ember then runs, through the Device's shell:

    nohup sh -c 'sleep 1; pid=$(cat /tmp/dropbear_koreader.pid); kill -TERM "$pid"; sleep 1;
      iptables -D INPUT -p tcp --dport <port> -m conntrack --ctstate NEW,ESTABLISHED -j ACCEPT;
      iptables -D OUTPUT -p tcp --sport <port> -m conntrack --ctstate ESTABLISHED -j ACCEPT;
      [ -d "/proc/$pid" ] || rm -f /tmp/dropbear_koreader.pid' >/dev/null 2>&1 &

(one line in the code). `<port>` is the Profile's port, an int formatted with `%d`;
nothing else in the command varies, so no user input reaches the shell. ember waits
up to three seconds for the shell to return (it returns at once, the work being in
the background), closes the link, and the Connector waits for "Search again"
without dialing on its own. "Disconnect only" skips the command.

## Why it mirrors the plugin

The command repeats `SSH:stopPlugin` from KOReader's SSH plugin, step for step,
without its force option:

- TERM only the pid in the plugin's pid file. That is the listening dropbear; every
  connection, ember's own included, is served by a forked child, which the TERM
  does not touch. This is the plugin's default (`force_kill_clients` off).
- Delete exactly the two rules `SSH:start` appends on a Kindle (`-A INPUT ... --dport`
  and `-A OUTPUT ... --sport`, same matches), so the firewall is back where it was.
- Remove the pid file once the process is gone: the plugin's `isRunning` is the pid
  file's existence, so its menu then shows the server as off and starting it again
  from the menu works.

Doing anything else (killing every dropbear, flushing the firewall) would differ
from what the user gets in KOReader and could break a setup ember does not know.

The command runs detached because deleting the OUTPUT rule can drop the packets of
the very connection that sent it: the Kindle's firewall only lets the SSH port's
established traffic out through that rule. `nohup`, the output sent to `/dev/null`
and the `&` let the outer shell return and the channel close at once; the first
`sleep 1` gives ember time to close the link cleanly before the rules go, and the
second gives dropbear the moment the plugin also waits for it to exit.

## Caveats

- Verified against the master source of `plugins/SSH.koplugin/main.lua`. The user's
  KOReader is v2025.08; the pid file path, the dropbear flags and the two rules have
  to be confirmed there, on a real Kindle.
- It relies on `nohup`, `sh`, `cat`, `kill`, `iptables` and `rm` on the Device
  (BusyBox provides them on jailbroken Kindles) and on ember logging in as root,
  which `iptables` needs.
- A port changed in KOReader after pairing, but not in ember's Profile, is the
  wrong port for the rules: they stay and only the TERM takes effect.
- If dropbear outlives the TERM, the pid file is kept, so KOReader still shows the
  server as running, which is then the truth.
- ember cannot start the server again; the user does that in KOReader.
