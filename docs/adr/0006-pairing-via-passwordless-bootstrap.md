# 6. Pairing through a one-time passwordless login

The Device's root password is unknown and KOReader's SSH server offers a "Login
without password" option. Pairing uses it once: ember connects, shows the host key
fingerprint for the user to confirm, appends its public key to
`/mnt/us/koreader/settings/SSH/authorized_keys`, verifies a key-only login, and then
tells the user to switch the option off. Users who never want the passwordless window
copy the public key over USB instead; the dashboard shows it ready to copy.

## Amendment: opt-in passwordless mode

Some users will keep "Login without password" on anyway. ember supports that as an
explicit Profile setting, off by default. Enabling it shows a confirmation dialog
stating the risk (root for anyone on the LAN while the SSH server is up) and only
takes effect after the user confirms. In this mode ember authenticates with a fixed,
non-secret password, the string `ember`, instead of its key, still enforces the
pinned host key, and the dashboard keeps a visible warning banner until the mode is
turned off. KOReader's patched dropbear accepts any password in this mode, so the
value protects nothing and is not a secret. It is not empty because SSH servers
handle a blank password as a case of its own (dropbear has a separate switch for
blank-password logins, `-B`), while KOReader's patch only changes how a password is
checked; a fixed word keeps ember on the path the patch covers whatever a build does
with blank ones. Verified on a Kindle running KOReader v2025.08.

## Amendment: automatic setup

The passwordless window is also used to survey the Device (KOReader version,
home folder, metadata mode, open book, free space) and to find it on the LAN by its
dropbear banner, so the user never types an address. The single required user action
is confirming the host key fingerprint of the found Device; no write happens before
it. After the key is installed, ember detects when the user has switched
passwordless login off.

Discovery logs into each dropbear host on the LAN with the passwordless login
read-only, so the candidate list can show what each device is before the user picks
one. ember's key is not offered and nothing is written until the confirmation.

## Amendment: the confirmation is enforced by the server

Passwordless mode cannot be switched on by a single request. The dashboard first
asks the server for a confirmation token, then shows the risk dialog, and only a
second request carrying that token turns the mode on. The server holds one token
at a time; it is random (256 bits), expires after two minutes, and any attempt to
use it spends it, right or wrong. The general settings request does not accept the
field at all. Turning the mode off is one request. Both still pass the same-origin
checks every write passes.
