"""First thing inside the sandbox: relay the proxy port to the broker's socket, run the command, and when it ends end the rest.
bwrap's init waits for all its children, so anything left behind would hold the sandbox open: nothing is, a run's shells end with it.
Run with python3 -I, so nothing writable from inside gets imported."""
import ctypes, os, signal, socket, sys, threading

def pump(a, b):
    try:
        while (d := a.recv(65536)): b.sendall(d)
    except OSError: pass
    try: b.shutdown(socket.SHUT_WR)
    except OSError: pass

def serve(c, path):
    u = socket.socket(socket.AF_UNIX)
    try: u.connect(path)
    except OSError: c.close(); return
    t = threading.Thread(target=pump, args=(u, c), daemon=True); t.start()
    pump(c, u); t.join(); u.close(); c.close()

def relay(ln, path):
    while True:
        c, _ = ln.accept()
        threading.Thread(target=serve, args=(c, path), daemon=True).start()

def main():
    path, port, argv = sys.argv[1], int(sys.argv[2]), sys.argv[4:]
    ln = socket.socket(); ln.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    ln.bind(("127.0.0.1", port)); ln.listen(64)
    kid = os.fork()
    if kid == 0:
        try: os.execvp(argv[0], argv)  # the listener is close-on-exec
        except OSError as x:
            print(f"sandbox: can't start {argv[0]}: {x}", file=sys.stderr); os._exit(127)
    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(sig, lambda n, _: os.kill(kid, n))
    null = os.open(os.devnull, os.O_RDWR)
    for fd in (0, 1, 2): os.dup2(null, fd)  # a held stdout would keep whoever reads the command's output waiting
    ctypes.CDLL(None).prctl(15, b"sandbox-fwd")
    threading.Thread(target=relay, args=(ln, path), daemon=True).start()
    while True:
        try: _, st = os.waitpid(kid, 0); break
        except InterruptedError: pass
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    try: os.kill(-1, signal.SIGKILL)  # everything else in this pid namespace; not this process, not init
    except OSError: pass
    os._exit(os.waitstatus_to_exitcode(st) & 255 if os.waitstatus_to_exitcode(st) >= 0 else 128 - os.waitstatus_to_exitcode(st))

main()
