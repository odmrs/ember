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
takes effect after the user confirms. In this mode ember authenticates with an empty
password instead of its key, still enforces the pinned host key, and the dashboard
keeps a visible warning banner until the mode is turned off.
