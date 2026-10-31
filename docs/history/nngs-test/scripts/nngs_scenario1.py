"""Scenario 1: match with counter-offer, observe, kibitz, undo, scoring."""
import sys
from nngs_client import Client

log = []
def login(name):
    c = Client(name, log=log)
    c.read(1.0)
    c.send(name, 1.5)
    return c

def step(title):
    log.append(("--", "STEP", title))

alice, bob, carol = login("alice"), login("bob"), login("carol")

step("who: list players")
carol.send("who", 1.0)

step("alice offers bob: alice black, 9x9, 1 min, 1 min byo-yomi")
alice.send("match bob B 9 1 1")
bob.read()
step("bob checks pending")
bob.send("pending")
step("bob counter-offers: 2 min main time")
bob.send("match alice W 9 2 1")
alice.read()
step("alice accepts the counter-offer by sending the identical terms")
alice.send("match bob B 9 2 1", 1.0)
bob.read()
carol.read()

step("carol lists games and observes game 1")
carol.send("games")
carol.send("observe 1", 1.0)

step("alice plays E5")
alice.send("E5")
bob.read(); carol.read()
step("alice tries to play again out of turn")
alice.send("D4")
step("bob plays C3")
bob.send("C3")
alice.read(); carol.read()

step("carol kibitzes; do players receive it?")
carol.send("kibitz bob should play tengen")
alice.read(); bob.read()
step("alice says something to bob")
alice.send("say good luck")
bob.read(); carol.read()

step("alice plays a bad move G7, then tries undo herself (not her turn)")
alice.send("G7")
bob.read()
alice.send("undo")
step("bob (on move) undoes alice's G7")
bob.send("undo")
alice.read(); carol.read()
step("alice plays G3 instead")
alice.send("G3")
bob.read(); carol.read()

step("time check")
bob.send("time")

step("bob passes, alice passes -> scoring")
bob.send("pass")
alice.send("pass", 1.0)
bob.read(); carol.read()
step("both type done")
alice.send("done")
bob.send("done", 1.0)
alice.read(); carol.read()

step("results / sgf listing")
alice.send("results", 1.0)
alice.send("sgf", 1.0)

for c in (alice, bob, carol):
    c.close()

with open(sys.argv[1], "w") as f:
    for who, d, text in log:
        if who == "--":
            f.write(f"\n==================== {text}\n")
        else:
            f.write(f"[{who} {d}] {text.rstrip()}\n")
