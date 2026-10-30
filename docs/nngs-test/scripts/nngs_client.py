"""Minimal NNGS telnet client for scripted testing."""
import socket
import time


class Client:
    def __init__(self, name, host="127.0.0.1", port=9696, log=None):
        self.name = name
        self.sock = socket.create_connection((host, port), timeout=5)
        self.sock.settimeout(0.2)
        self.log = log if log is not None else []
        self.buf = ""

    def read(self, wait=0.6):
        end = time.time() + wait
        out = ""
        while time.time() < end:
            try:
                data = self.sock.recv(65536)
                if not data:
                    break
                out += data.decode("latin-1")
            except socket.timeout:
                pass
        if out:
            self.log.append((self.name, "<<", out))
        self.buf += out
        return out

    def send(self, line, wait=0.6):
        self.log.append((self.name, ">>", line))
        self.sock.sendall((line + "\r\n").encode())
        return self.read(wait)

    def close(self):
        self.sock.close()
