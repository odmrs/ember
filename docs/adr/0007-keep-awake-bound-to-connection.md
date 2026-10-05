# 7. Keep-awake bound to the SSH connection

The Device drops Wi-Fi when it sleeps, which kills the Session. While connected,
ember runs one remote command that sets `preventScreenSaver` to 1, blocks reading
stdin, and sets it back to 0 when stdin closes. Because the reset is triggered by the
SSH channel closing, a crashed or killed ember still leaves the Device able to sleep.

## Amendment: the command and its failures

The command is the fixed string
`lipc-set-prop com.lab126.powerd preventScreenSaver 1 || exit $?; cat >/dev/null; lipc-set-prop com.lab126.powerd preventScreenSaver 0`.
Nothing from the Profile or the user is put into it. The `|| exit $?` makes a
Device without `lipc-set-prop` (exit 127), or one that refuses the property, end
the command at once instead of blocking in `cat`. That, or a Device that refuses
to start the command at all, is a failure: ember logs it once, shows "keep-awake
unavailable" and does not try again, on that link or the next, until the user
switches keep-awake off and on.

A command that ends with status 0 is not a failure: it ran to its end because
something on the Device closed its input, and its last step allowed the screen
saver again. ember shows keep-awake as off for that link and starts the command
again on the next link (or when Settings are saved), without marking the Device
unavailable.

On unlink (the last tab closed, or ember quitting normally) ember closes the
command's stdin and waits up to three seconds for it to finish before closing the
connection, so the reset runs while ember is still there to see it. Switching
keep-awake off in Settings does the same on the live link. If ember is killed,
the connection closes, the Device's SSH server closes the command's stdin, and
`cat` returning runs the reset. That last path depends on how the Device's
dropbear treats a command whose channel goes away; the fake Device in the tests
models it, and it still has to be confirmed on a real Kindle.
