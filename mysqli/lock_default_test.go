package mysqli

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestLockTablesAliasDoesNotAuthorizeHomonymousTable vérifie qu'un alias de LOCK TABLES homonyme d'une autre
// table ne verrouille pas cette table : l'autorisateur reçoit de SQLite le nom réel, seul inscrit dans
// l'ensemble verrouillé, et l'écriture dans la table homonyme est refusée par 1100 sans laisser de ligne.
func TestLockTablesAliasDoesNotAuthorizeHomonymousTable(t *testing.T) {
	p := newPHP(t)
	link := p.connectSQLite(filepath.Join(t.TempDir(), "alias.sqlite"))
	p.query(link, "CREATE TABLE t1 (id INT)")
	p.query(link, "CREATE TABLE t2 (id INT)")

	p.query(link, "LOCK TABLES t1 AS t2 WRITE")
	if n := p.sqliteErrno(link, "INSERT INTO t2 VALUES (1)"); n != 1100 {
		t.Fatalf("INSERT INTO t2 sous LOCK TABLES t1 AS t2 WRITE : errno %d ; attendu 1100", n)
	}
	if msg := p.str(p.call("mysqli_error", link)); msg != "Table 't2' was not locked with LOCK TABLES" {
		t.Fatalf("message du refus = %q", msg)
	}
	p.query(link, "UNLOCK TABLES")
	if rows := p.rows(link, "SELECT COUNT(*) FROM t2"); len(rows) != 1 || rows[0][0] != "0" {
		t.Fatalf("t2 après le refus = %q ; attendu 0 ligne", rows)
	}

	p.query(link, "LOCK TABLES t1 AS a WRITE")
	p.query(link, "INSERT INTO t1 VALUES (7)")
	if rows := p.rows(link, "SELECT id FROM t1 AS a"); len(rows) != 1 || rows[0][0] != "7" {
		t.Fatalf("SELECT id FROM t1 AS a sous LOCK TABLES t1 AS a WRITE = %q ; attendu 7", rows)
	}
	if n := p.sqliteErrno(link, "SELECT id FROM t2"); n != 1100 {
		t.Fatalf("lecture de t2 sous LOCK TABLES t1 AS a WRITE : errno %d ; attendu 1100", n)
	}
	p.query(link, "UNLOCK TABLES")
	p.reset()
}

// TestAlterColumnSetAndDropDefaultAppliesSchema vérifie qu'ALTER [COLUMN] col SET DEFAULT et DROP DEFAULT
// changent la valeur par défaut réellement appliquée par SQLite, en conservant lignes, nullabilité et
// compteur d'auto-incrément, et que colonne inconnue et clause inconnue sont refusées par 1054 et 1064.
func TestAlterColumnSetAndDropDefaultAppliesSchema(t *testing.T) {
	p := newPHP(t)
	link := p.connectSQLite(filepath.Join(t.TempDir(), "default.sqlite"))
	p.query(link, "CREATE TABLE t (id INT NOT NULL AUTO_INCREMENT, c VARCHAR(10) DEFAULT 'vieux', n INT NOT NULL DEFAULT 3, PRIMARY KEY (id))")
	p.query(link, "INSERT INTO t (c) VALUES ('a')")

	p.query(link, "ALTER TABLE t ALTER COLUMN c SET DEFAULT 'neuf'")
	p.query(link, "INSERT INTO t (n) VALUES (5)")
	if rows := p.rows(link, "SELECT id, c, n FROM t ORDER BY id"); !slices.EqualFunc(rows, [][]string{
		{"1", "a", "3"}, {"2", "neuf", "5"},
	}, slices.Equal) {
		t.Fatalf("lignes après SET DEFAULT 'neuf' = %q", rows)
	}
	if desc := p.rows(link, "DESCRIBE t"); len(desc) != 3 || desc[1][0] != "c" || desc[1][1] != "varchar(10)" || desc[1][2] != "YES" || desc[1][4] != "neuf" {
		t.Fatalf("DESCRIBE t après SET DEFAULT = %q", desc)
	}

	p.query(link, "ALTER TABLE t ALTER c DROP DEFAULT, ALTER COLUMN n SET DEFAULT (40 + 2)")
	p.query(link, "INSERT INTO t (id) VALUES (10)")
	if rows := p.rows(link, "SELECT c, n FROM t WHERE id = 10"); len(rows) != 1 || rows[0][0] != "<NULL>" || rows[0][1] != "42" {
		t.Fatalf("ligne après DROP DEFAULT et SET DEFAULT (40 + 2) = %q ; attendu NULL et 42", rows)
	}
	p.query(link, "ALTER TABLE t ALTER n DROP DEFAULT")
	if n := p.sqliteErrno(link, "INSERT INTO t (c) VALUES ('z')"); n != 1048 {
		t.Fatalf("n NOT NULL sans défaut omis : errno %d ; attendu 1048 (nullabilité conservée)", n)
	}
	p.query(link, "INSERT INTO t (c, n) VALUES ('z', 1)")
	if rows := p.rows(link, "SELECT id FROM t WHERE c = 'z'"); len(rows) != 1 || rows[0][0] != "11" {
		t.Fatalf("id après reconstructions = %q ; attendu 11 (auto-incrément conservé)", rows)
	}

	before := p.rows(link, "SELECT id, c, n FROM t ORDER BY id")
	if n := p.sqliteErrno(link, "ALTER TABLE t ALTER COLUMN absente SET DEFAULT 1"); n != 1054 {
		t.Fatalf("SET DEFAULT sur une colonne inconnue : errno %d ; attendu 1054", n)
	}
	for _, sql := range []string{"ALTER TABLE t ALTER c SET VISIBLE", "ALTER TABLE t ALTER c DROP", "ALTER TABLE t ALTER c SET DEFAULT 'x' 'y'", "ALTER TABLE t ALTER c"} {
		if n := p.sqliteErrno(link, sql); n != 1064 {
			t.Errorf("%q : errno %d ; attendu 1064", sql, n)
		}
	}
	if after := p.rows(link, "SELECT id, c, n FROM t ORDER BY id"); !slices.EqualFunc(before, after, slices.Equal) {
		t.Fatalf("table altérée par un ALTER refusé : %q ; attendu %q", after, before)
	}
	p.reset()
}

// TestFlushTablesWithReadLockBlocksWritesUntilUnlock vérifie que FLUSH TABLES WITH READ LOCK refuse par 1223
// toute écriture et toute modification de schéma, y compris sous START TRANSACTION et LOCK TABLES ... WRITE,
// en laissant les lectures passer, jusqu'à UNLOCK TABLES ; puis que FLUSH, RESET et PURGE valident leur
// syntaxe par 1064.
func TestFlushTablesWithReadLockBlocksWritesUntilUnlock(t *testing.T) {
	p := newPHP(t)
	link := p.connectSQLite(filepath.Join(t.TempDir(), "flush.sqlite"))
	p.query(link, "CREATE TABLE t (id INT NOT NULL, v VARCHAR(10), PRIMARY KEY (id))")
	p.query(link, "CREATE TABLE u (id INT)")
	p.query(link, "INSERT INTO t VALUES (1, 'a')")

	for _, lock := range []string{"FLUSH TABLES WITH READ LOCK", "FLUSH LOCAL TABLES WITH READ LOCK"} {
		p.query(link, lock)
		if rows := p.rows(link, "SELECT v FROM t JOIN u ON 1 = 1 UNION ALL SELECT v FROM t"); len(rows) != 1 || rows[0][0] != "a" {
			t.Fatalf("lecture sous %s = %q", lock, rows)
		}
		for _, sql := range []string{
			"INSERT INTO t VALUES (2, 'b')", "UPDATE t SET v = 'x'", "DELETE FROM t", "REPLACE INTO u VALUES (1)",
			"TRUNCATE TABLE u", "CREATE TABLE w (id INT)", "DROP TABLE u", "ALTER TABLE t ADD COLUMN w INT",
			"CREATE INDEX iv ON t (v)", "CREATE DATABASE autre", "LOCK TABLES t WRITE",
		} {
			if n := p.sqliteErrno(link, sql); n != 1223 {
				t.Errorf("%q sous %s : errno %d ; attendu 1223", sql, lock, n)
			}
		}
		p.query(link, "START TRANSACTION")
		if n := p.sqliteErrno(link, "INSERT INTO t VALUES (3, 'c')"); n != 1223 {
			t.Errorf("INSERT après START TRANSACTION sous %s : errno %d ; attendu 1223", lock, n)
		}
		p.query(link, "COMMIT")
		p.query(link, "LOCK TABLES t READ")
		if n := p.sqliteErrno(link, "SELECT * FROM u"); n != 1100 {
			t.Errorf("lecture de u sous LOCK TABLES t READ après %s : errno %d ; attendu 1100", lock, n)
		}
		if n := p.sqliteErrno(link, "UPDATE t SET v = 'y'"); n != 1223 {
			t.Errorf("UPDATE de t sous LOCK TABLES t READ après %s : errno %d ; attendu 1223", lock, n)
		}
		if n := p.sqliteErrno(link, "FLUSH TABLES WITH READ LOCK"); n != 1192 {
			t.Errorf("FLUSH TABLES WITH READ LOCK sous LOCK TABLES : errno %d ; attendu 1192", n)
		}
		p.query(link, "UNLOCK TABLES")
		p.query(link, "INSERT INTO u VALUES (9)")
		p.query(link, "DELETE FROM u")
	}
	if rows := p.rows(link, "SELECT id, v FROM t"); len(rows) != 1 || rows[0][0] != "1" || rows[0][1] != "a" {
		t.Fatalf("t après les écritures refusées = %q", rows)
	}
	if tables := p.rows(link, "SHOW TABLES"); len(tables) != 2 {
		t.Fatalf("SHOW TABLES après les créations refusées = %q", tables)
	}

	p.query(link, "FLUSH TABLES t WITH READ LOCK")
	if n := p.sqliteErrno(link, "INSERT INTO t VALUES (4, 'd')"); n != 1099 {
		t.Errorf("INSERT INTO t sous FLUSH TABLES t WITH READ LOCK : errno %d ; attendu 1099", n)
	}
	if n := p.sqliteErrno(link, "SELECT * FROM u"); n != 1100 {
		t.Errorf("lecture de u sous FLUSH TABLES t WITH READ LOCK : errno %d ; attendu 1100", n)
	}
	p.query(link, "UNLOCK TABLES")
	if n := p.sqliteErrno(link, "FLUSH TABLES absente WITH READ LOCK"); n != 1146 {
		t.Errorf("FLUSH TABLES absente WITH READ LOCK : errno %d ; attendu 1146", n)
	}

	for _, sql := range []string{
		"FLUSH TABLES", "FLUSH TABLES t, u", "FLUSH PRIVILEGES", "FLUSH LOGS", "FLUSH STATUS", "FLUSH HOSTS",
		"FLUSH NO_WRITE_TO_BINLOG BINARY LOGS, SLOW LOGS", "RESET QUERY CACHE", "RESET PERSIST",
	} {
		// Peuple le cache de métadonnées de colonnes par une sélection préalable, puis vérifie que l'instruction le vide.
		p.query(link, "SELECT id, v FROM t")
		cid, _ := objectHandle(p.z, link, ClassIDMysqli)
		c := getRegistry(p.z).conns[cid]
		sb := c.be.(*sqliteBackend)
		if len(sb.colMetaCache) == 0 {
			t.Fatalf("avant %q : cache colMetaCache vide après SELECT", sql)
		}
		if n := p.sqliteErrno(link, sql); n != 0 {
			t.Errorf("%q : errno %d ; attendu succès", sql, n)
		}
		if len(sb.colMetaCache) != 0 {
			t.Errorf("après %q : cache colMetaCache non vidé (%d entrées)", sql, len(sb.colMetaCache))
		}
	}
	for _, sql := range []string{
		"RESET MASTER", "RESET SLAVE", "RESET REPLICA", "RESET REPLICA ALL",
		"PURGE BINARY LOGS TO 'mysql-bin.000010'", "PURGE MASTER LOGS BEFORE '2026-10-15 00:00:00'",
	} {
		if n := p.sqliteErrno(link, sql); n != 1235 {
			t.Errorf("%q : errno %d ; attendu 1235 (ER_NOT_SUPPORTED_YET)", sql, n)
		}
	}
	for _, sql := range []string{
		"FLUSH", "FLUSH LOCAL", "FLUSH CACHE", "FLUSH BINARY", "FLUSH PRIVILEGES,", "FLUSH TABLES WITH READ",
		"FLUSH TABLES FOR EXPORT", "RESET", "RESET CACHE", "PURGE BINARY LOGS", "PURGE BINARY LOGS TO", "PURGE LOGS TO 'x'",
	} {
		if n := p.sqliteErrno(link, sql); n != 1064 {
			t.Errorf("%q : errno %d ; attendu 1064", sql, n)
		}
	}
	p.query(link, "INSERT INTO t VALUES (5, 'e')")
	p.reset()
}
