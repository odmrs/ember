# 7. Keep-awake bound to the SSH connection

The Device drops Wi-Fi when it sleeps, which kills the Session. While connected,
ember runs one remote command that sets `preventScreenSaver` to 1, blocks reading
stdin, and sets it back to 0 when stdin closes. Because the reset is triggered by the
SSH channel closing, a crashed or killed ember still leaves the Device able to sleep.
