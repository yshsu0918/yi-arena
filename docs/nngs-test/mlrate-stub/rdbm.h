/* Stub replacing mlrate's rdbm.h: no ratings database. */
#ifndef RDBM_H
#define RDBM_H
typedef struct rdbm *rdbm_t;
typedef struct {
  char name[32];
  char rank[8];
  int star;
  double rating, low, high;
  unsigned wins, losses;
  int h;
} rdbm_player_t;
rdbm_t rdbm_open(const char *name, int rw);
int rdbm_fetch(rdbm_t db, const char *name, rdbm_player_t *rp);
void rdbm_close(rdbm_t db);
#endif
