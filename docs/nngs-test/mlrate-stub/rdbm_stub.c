#include <stddef.h>
#include "rdbm.h"
rdbm_t rdbm_open(const char *name, int rw) { (void)name; (void)rw; return NULL; }
int rdbm_fetch(rdbm_t db, const char *name, rdbm_player_t *rp) { (void)db; (void)name; (void)rp; return 0; }
void rdbm_close(rdbm_t db) { (void)db; }
