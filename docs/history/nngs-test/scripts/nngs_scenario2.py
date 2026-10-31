"""Scenario 2: automatic timeout with no moves; disconnect and resume."""
import sys
import time
from nngs_client import Client

log = []
def login(name):
    c = Client(name, log=log)
    c.read(1.0)
    c.send(name, 1.5)
    return c

def step(title):
    log.append(("--", "STEP", title))

# --- timeout: 1 minute main time, no byo-yomi; nobody moves
dave, erin = login("dave"), login("erin")
step("dave offers erin 9x9, 1 min, 0 byo-yomi; erin accepts")
dave.send("match erin B 9 1 0")
erin.send("match dave W 9 1 0", 1.0)
dave.read()

# --- disconnect: frank and gina start a game, frank drops
frank, gina = login("frank"), login("gina")
step("frank vs gina 9x9, 10 min, 10 min byo-yomi; frank plays E5")
frank.send("match gina B 9 10 10")
gina.send("match frank W 9 10 10", 1.0)
frank.read()
frank.send("E5")
gina.read()
step("frank's connection drops")
frank.close()
gina.read(2.0)
step("gina lists stored games")
gina.send("stored")

step("frank reconnects and loads the game")
frank = login("frank")
frank.send("load gina-frank", 1.5)
gina.read()

step("waiting 75 s without any move in dave-erin (no 'time' command)")
t0 = time.time()
while time.time() - t0 < 75:
    dave.read(5); erin.read(0.1)
step("after waiting: games list")
erin.send("games")

for c in (dave, erin, frank, gina):
    c.close()

with open(sys.argv[1], "w") as f:
    for who, d, text in log:
        if who == "--":
            f.write(f"\n==================== {text}\n")
        else:
            f.write(f"[{who} {d}] {text.rstrip()}\n")
