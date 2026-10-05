# 3. Key-only auth, pinned host key

At Pairing, ember generates its own ed25519 key pair in the Profile (private key
file mode 0600) and installs the public key on the Device. From then on ember never
stores or sends a password. The Device host key fingerprint is recorded at Pairing
and every later connection must match it, which also makes network scanning safe:
a host on the LAN that answers on the port but has a different key is ignored.

## Amendment: forgetting a Device discards the key pair

"Forget this Kindle" in Settings deletes the Profile and ember's key pair
together, and the next Pairing generates a new pair. Keeping the old key would
leave a file on the computer that still opens root on a Device the user asked
ember to forget. The forgotten Device keeps the old public key in its
authorized_keys, where it opens nothing once the private half is gone; ember says
so and leaves removing the line to the user, since it can no longer log in to do
it. The cost is that pairing the same Kindle again needs the passwordless window,
or the USB copy of the new public key, once more.
