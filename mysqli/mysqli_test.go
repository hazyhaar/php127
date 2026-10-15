package mysqli

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// php porte une Zone de requête et les appels natifs du paquet, comme les invoquerait la VM.
type php struct {
	t   *testing.T
	z   *zone.Zone
	out bytes.Buffer
}

func newPHP(t *testing.T) *php {
	t.Helper()
	return &php{t: t, z: zone.New(zone.DefaultCapacity)}
}

func (p *php) s(text string) types.Value {
	p.t.Helper()
	v, err := newStr(p.z, text)
	if err != nil {
		p.t.Fatal(err)
	}
	return v
}

func (p *php) callErr(name string, args ...types.Value) (types.Value, error) {
	p.t.Helper()
	fn, ok := Lookup(name)
	if !ok {
		p.t.Fatalf("fonction %s absente de Lookup", name)
	}
	return fn(p.z, &p.out, args)
}

func (p *php) call(name string, args ...types.Value) types.Value {
	p.t.Helper()
	v, err := p.callErr(name, args...)
	if err != nil {
		p.t.Fatalf("%s: %v", name, err)
	}
	return v
}

func (p *php) str(v types.Value) string {
	p.t.Helper()
	v = types.Deref(p.z, v)
	if v.Type != types.TypeString {
		p.t.Fatalf("chaîne attendue, type %d reçu", v.Type)
	}
	return types.StringData(p.z, v)
}

func (p *php) isTrue(v types.Value) bool { return types.Deref(p.z, v).Type == types.TypeTrue }

func (p *php) query(link types.Value, sql string) types.Value {
	p.t.Helper()
	v := p.call("mysqli_query", link, p.s(sql))
	if types.Deref(p.z, v).Type == types.TypeFalse {
		p.t.Fatalf("mysqli_query(%q) = false : %d %s", sql, p.call("mysqli_errno", link).ToLong(), p.str(p.call("mysqli_error", link)))
	}
	return v
}

// cellText rend une valeur PHP scalaire en texte ; NULL devient « <NULL> ».
func (p *php) cellText(v types.Value) string {
	v = types.Deref(p.z, v)
	switch v.Type {
	case types.TypeNull:
		return "<NULL>"
	case types.TypeString:
		return types.StringData(p.z, v)
	case types.TypeLong:
		return "int:" + strconv.FormatInt(v.ToLong(), 10)
	case types.TypeDouble:
		return "float:" + strconv.FormatFloat(v.ToDouble(), 'g', -1, 64)
	}
	return "?"
}

// rows lit toutes les lignes d'une requête en mode MYSQLI_NUM.
func (p *php) rows(link types.Value, sql string) [][]string {
	p.t.Helper()
	res := p.query(link, sql)
	all := p.call("mysqli_fetch_all", res, types.NewLong(modeNum))
	var out [][]string
	types.ArrayForEach(p.z, all, func(_, row types.Value) bool {
		var r []string
		types.ArrayForEach(p.z, types.Deref(p.z, row), func(_, c types.Value) bool {
			r = append(r, p.cellText(c))
			return true
		})
		out = append(out, r)
		return true
	})
	p.call("mysqli_free_result", res)
	return out
}

func (p *php) key(arr types.Value, k string) types.Value {
	p.t.Helper()
	kv := p.s(k)
	v, found, err := types.ArrayGet(p.z, types.Deref(p.z, arr), kv, warnDrop)
	if err != nil || !found {
		p.t.Fatalf("clé %q absente", k)
	}
	return v
}

func (p *php) prop(obj types.Value, name string) types.Value {
	p.t.Helper()
	v, found, err := GetProperty(p.z, obj, name)
	if err != nil || !found {
		p.t.Fatalf("propriété %s : found=%v err=%v", name, found, err)
	}
	return v
}

func (p *php) objProp(obj types.Value, name string) types.Value {
	p.t.Helper()
	v, ok := ObjectGetProperty(p.z, obj, name)
	if !ok {
		p.t.Fatalf("propriété dynamique %s absente", name)
	}
	return v
}

func (p *php) reset() {
	p.z.Reset(CleanupDispatcher(p.z))
}

func (p *php) connectSQLite(path string) types.Value {
	p.t.Helper()
	p.call("mysqli_report", types.NewLong(ReportOff))
	link := p.call("mysqli_init")
	ok := p.call("mysqli_real_connect", link, p.s("sqlite:"+path), p.s("wp"), p.s("secret"), types.NewNull(), types.NewNull(), types.NewNull(), types.NewLong(0))
	if !p.isTrue(ok) {
		p.t.Fatalf("connexion SQLite refusée : %s", p.cellText(p.prop(link, "connect_error")))
	}
	return link
}

const wpOptionsDDL = `CREATE TABLE wp_options (
	option_id bigint(20) unsigned NOT NULL auto_increment,
	option_name varchar(191) NOT NULL default '',
	option_value longtext NOT NULL,
	autoload varchar(20) NOT NULL default 'yes',
	PRIMARY KEY  (option_id),
	UNIQUE KEY option_name (option_name),
	KEY autoload (autoload)
) DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_520_ci`

const wpPostmetaDDL = `CREATE TABLE wp_postmeta (
	meta_id bigint(20) unsigned NOT NULL auto_increment,
	post_id bigint(20) unsigned NOT NULL default '0',
	meta_key varchar(255) default NULL,
	meta_value longtext,
	PRIMARY KEY  (meta_id),
	KEY post_id (post_id),
	KEY meta_key (meta_key(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci`

func TestWPDBFlowOnSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wordpress.sqlite")
	p := newPHP(t)
	link := p.connectSQLite(path)
	if n := p.prop(link, "connect_errno").ToLong(); n != 0 {
		t.Fatalf("connect_errno = %d", n)
	}
	if !p.isTrue(p.call("mysqli_set_charset", link, p.s("utf8mb4"))) {
		t.Fatal("mysqli_set_charset a échoué")
	}
	if cs := p.str(p.call("mysqli_character_set_name", link)); cs != "utf8mb4" {
		t.Fatalf("character_set_name = %q", cs)
	}

	res := p.query(link, "SELECT @@SESSION.sql_mode")
	row := p.call("mysqli_fetch_array", res)
	if got := p.str(p.key(row, "@@SESSION.sql_mode")); got != "STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION" {
		t.Fatalf("sql_mode = %q", got)
	}
	p.query(link, "SET SESSION sql_mode='NO_ZERO_IN_DATE,NO_ENGINE_SUBSTITUTION'")
	if got := p.rows(link, "SELECT @@SESSION.sql_mode"); got[0][0] != "NO_ZERO_IN_DATE,NO_ENGINE_SUBSTITUTION" {
		t.Fatalf("sql_mode après SET = %v", got)
	}
	p.query(link, "SET NAMES 'utf8mb4' COLLATE 'utf8mb4_unicode_520_ci'")
	p.query(link, "SET SQL_BIG_SELECTS=1")
	if !p.isTrue(p.call("mysqli_select_db", link, p.s("wordpress"))) {
		t.Fatal("mysqli_select_db a échoué")
	}
	if !p.isTrue(p.query(link, "DO 1")) {
		t.Fatal("DO 1 a échoué")
	}
	if v := types.Deref(p.z, p.call("mysqli_query", link, p.s("SELECT @@no_such_variable"))); v.Type != types.TypeFalse {
		t.Fatal("variable système inconnue acceptée")
	}
	if n := p.call("mysqli_errno", link).ToLong(); n != 1193 {
		t.Fatalf("errno variable inconnue = %d", n)
	}

	p.query(link, wpOptionsDDL)
	p.query(link, wpPostmetaDDL)
	if got := p.rows(link, "SHOW TABLES LIKE 'wp\\_%'"); len(got) != 2 || got[0][0] != "wp_options" || got[1][0] != "wp_postmeta" {
		t.Fatalf("SHOW TABLES = %v", got)
	}
	desc := p.rows(link, "DESCRIBE wp_options")
	want := [][]string{
		{"option_id", "bigint(20) unsigned", "NO", "PRI", "<NULL>", "auto_increment"},
		{"option_name", "varchar(191)", "NO", "UNI", "", ""},
		{"option_value", "longtext", "NO", "", "<NULL>", ""},
		{"autoload", "varchar(20)", "NO", "MUL", "yes", ""},
	}
	if len(desc) != len(want) {
		t.Fatalf("DESCRIBE = %v", desc)
	}
	for i := range want {
		if strings.Join(desc[i], "|") != strings.Join(want[i], "|") {
			t.Fatalf("DESCRIBE ligne %d = %v, attendu %v", i, desc[i], want[i])
		}
	}
	full := p.rows(link, "SHOW FULL COLUMNS FROM `wp_options`")
	if full[1][2] != "utf8mb4_unicode_520_ci" || full[0][2] != "<NULL>" {
		t.Fatalf("SHOW FULL COLUMNS collation = %v / %v", full[1], full[0])
	}
	idx := p.rows(link, "SHOW INDEX FROM wp_postmeta")
	keys := make([]string, 0, len(idx))
	for _, r := range idx {
		keys = append(keys, r[2]+":"+r[4])
	}
	if strings.Join(keys, ",") != "PRIMARY:meta_id,post_id:post_id,meta_key:meta_key" {
		t.Fatalf("SHOW INDEX = %v", keys)
	}

	p.query(link, "INSERT INTO wp_options (option_name, option_value, autoload) VALUES ('siteurl', 'http://a.test', 'yes')")
	if id := p.call("mysqli_insert_id", link).ToLong(); id != 1 {
		t.Fatalf("insert_id = %d", id)
	}
	if n := p.call("mysqli_affected_rows", link).ToLong(); n != 1 {
		t.Fatalf("affected_rows = %d", n)
	}
	raw := "It's a \"test\"\\ \n\r\x00\x1a end"
	esc := p.str(p.call("mysqli_real_escape_string", link, p.s(raw)))
	if esc != `It\'s a \"test\"\\ \n\r\0\Z end` {
		t.Fatalf("échappement = %q", esc)
	}
	p.query(link, "INSERT INTO wp_options (option_name, option_value) VALUES ('blogname', '"+esc+"')")
	if got := p.rows(link, "SELECT option_value, autoload FROM wp_options WHERE option_name = \"BLOGNAME\""); len(got) != 1 || got[0][0] != raw || got[0][1] != "yes" {
		t.Fatalf("relecture de la chaîne échappée (comparaison insensible à la casse) = %q", got)
	}

	p.query(link, "INSERT INTO `wp_options` (`option_name`, `option_value`, `autoload`) VALUES ('siteurl', 'http://b.test', 'no') ON DUPLICATE KEY UPDATE `option_name` = VALUES(`option_name`), `option_value` = VALUES(`option_value`), `autoload` = VALUES(`autoload`)")
	if n := p.call("mysqli_affected_rows", link).ToLong(); n != 2 {
		t.Fatalf("affected_rows après ON DUPLICATE KEY UPDATE = %d", n)
	}
	if got := p.rows(link, "SELECT option_id, option_value, autoload FROM wp_options WHERE option_name = 'siteurl'"); got[0][0] != "1" || got[0][1] != "http://b.test" || got[0][2] != "no" {
		t.Fatalf("upsert = %v", got)
	}
	if v := types.Deref(p.z, p.call("mysqli_query", link, p.s("INSERT INTO wp_options (option_name, option_value) VALUES ('siteurl', 'x')"))); v.Type != types.TypeFalse {
		t.Fatal("doublon accepté")
	}
	if n := p.call("mysqli_errno", link).ToLong(); n != 1062 {
		t.Fatalf("errno doublon = %d (%s)", n, p.str(p.call("mysqli_error", link)))
	}
	if st := p.str(p.call("mysqli_sqlstate", link)); st != "23000" {
		t.Fatalf("sqlstate doublon = %s", st)
	}
	p.query(link, "INSERT IGNORE INTO wp_options (option_name, option_value) VALUES ('siteurl', 'ignored')")
	if n := p.call("mysqli_affected_rows", link).ToLong(); n != 0 {
		t.Fatalf("INSERT IGNORE affected_rows = %d", n)
	}
	p.query(link, "REPLACE INTO wp_options (option_name, option_value) VALUES ('home', 'http://h.test')")

	for i := range 5 {
		p.query(link, "INSERT INTO wp_options (option_name, option_value, autoload) VALUES ('opt"+strconv.Itoa(i)+"', '"+strconv.Itoa(i)+"', 'no')")
	}
	p.query(link, "INSERT INTO wp_options (option_name, option_value) VALUES ('_transient_x', '1'), ('xtransientx', '2')")
	page := p.rows(link, "SELECT SQL_CALC_FOUND_ROWS option_name FROM wp_options WHERE autoload = 'no' ORDER BY option_id LIMIT 2")
	if len(page) != 2 || page[0][0] != "siteurl" || page[1][0] != "opt0" {
		t.Fatalf("page SQL_CALC_FOUND_ROWS = %v", page)
	}
	if got := p.rows(link, "SELECT FOUND_ROWS()"); got[0][0] != "6" {
		t.Fatalf("FOUND_ROWS() = %v", got)
	}

	res = p.query(link, "SELECT option_id, option_name, option_value FROM wp_options WHERE option_id = 1")
	if n := p.call("mysqli_num_fields", res).ToLong(); n != 3 {
		t.Fatalf("num_fields = %d", n)
	}
	f := p.call("mysqli_fetch_field", res)
	if p.str(p.objProp(f, "name")) != "option_id" || p.str(p.objProp(f, "table")) != "wp_options" || p.str(p.objProp(f, "db")) != "wordpress" {
		t.Fatal("métadonnées de champ inattendues")
	}
	if typ := p.objProp(f, "type").ToLong(); typ != typeLongLong {
		t.Fatalf("type = %d", typ)
	}
	if flags := p.objProp(f, "flags").ToLong(); flags&(flagPriKey|flagAutoIncrement|flagNotNull) != flagPriKey|flagAutoIncrement|flagNotNull {
		t.Fatalf("flags = %d", flags)
	}
	f2 := p.call("mysqli_fetch_field", res)
	if p.str(p.objProp(f2, "name")) != "option_name" || p.objProp(f2, "type").ToLong() != typeVarString || p.objProp(f2, "length").ToLong() != 764 {
		t.Fatal("métadonnées varchar inattendues")
	}
	obj := p.call("mysqli_fetch_object", res)
	if id := p.objProp(obj, "option_id"); types.Deref(p.z, id).Type != types.TypeString || p.str(id) != "1" {
		t.Fatal("protocole texte : option_id doit être la chaîne \"1\"")
	}
	if p.str(p.objProp(obj, "option_value")) != "http://b.test" {
		t.Fatal("fetch_object : option_value inattendu")
	}
	if v := types.Deref(p.z, p.call("mysqli_fetch_object", res)); v.Type != types.TypeNull {
		t.Fatal("fin de résultat : NULL attendu")
	}
	if n := p.prop(res, "num_rows").ToLong(); n != 1 {
		t.Fatalf("num_rows = %d", n)
	}

	if got := p.rows(link, `SELECT COUNT(*) FROM wp_options WHERE option_name LIKE '\_transient\_%'`); got[0][0] != "1" {
		t.Fatalf("LIKE avec échappement = %v", got)
	}
	exprs := p.rows(link, "SELECT IF(1 > 0, 'a', 'b'), CONCAT('a', 'b', 1), UNIX_TIMESTAMP() > 1000000000, SUBSTRING_INDEX('a.b.c', '.', 2), 'abc' REGEXP '^A', NOW() <> '', DATE_ADD('2024-01-31 10:00:00', INTERVAL 1 DAY), 1 || 0, NULL <=> NULL")
	if strings.Join(exprs[0], "|") != "a|ab1|1|a.b|1|1|2024-02-01 10:00:00|1|1" {
		t.Fatalf("fonctions traduites = %v", exprs[0])
	}
	p.query(link, "DELETE a, b FROM wp_options a, wp_options b WHERE a.option_name = '_transient_x' AND b.option_name = 'xtransientx'")
	if n := p.call("mysqli_affected_rows", link).ToLong(); n != 2 {
		t.Fatalf("DELETE multi-tables affected_rows = %d", n)
	}
	if !p.isTrue(p.call("mysqli_multi_query", link, p.s("SELECT 1; SELECT 'deux'; UPDATE wp_options SET autoload = 'no' WHERE option_name = 'home'"))) {
		t.Fatal("multi_query a échoué")
	}
	var seen []string
	for {
		if r := types.Deref(p.z, p.call("mysqli_store_result", link)); r.Type == types.TypeObject {
			seen = append(seen, p.cellText(p.call("mysqli_fetch_column", r)))
		} else {
			seen = append(seen, "ok:"+strconv.FormatInt(p.call("mysqli_affected_rows", link).ToLong(), 10))
		}
		if !p.isTrue(p.call("mysqli_more_results", link)) {
			break
		}
		p.call("mysqli_next_result", link)
	}
	if strings.Join(seen, ",") != "1,deux,ok:1" {
		t.Fatalf("multi_query = %v", seen)
	}
	total := p.rows(link, "SELECT COUNT(*) FROM wp_options")[0][0]
	if !p.isTrue(p.call("mysqli_close", link)) {
		t.Fatal("mysqli_close a échoué")
	}
	if _, err := p.callErr("mysqli_query", link, p.s("SELECT 1")); err == nil || err.Error() != "mysqli object is already closed" {
		t.Fatalf("requête après fermeture : %v", err)
	}
	p.reset()

	// Persistance réelle : une autre requête rouvre le fichier et relit les lignes validées.
	p2 := newPHP(t)
	link2 := p2.connectSQLite(path)
	if got := p2.rows(link2, "SELECT COUNT(*) FROM wp_options"); got[0][0] != total || total != "8" {
		t.Fatalf("relecture après réouverture = %v, attendu %s (8)", got, total)
	}
	p2.reset()
}

func TestPreparedStatementsOnSQLite(t *testing.T) {
	p := newPHP(t)
	link := p.connectSQLite(":memory:")
	p.query(link, "CREATE TABLE wp_posts (ID bigint(20) unsigned NOT NULL AUTO_INCREMENT, post_title text NOT NULL, menu_order int(11) NOT NULL DEFAULT 0, score double DEFAULT NULL, PRIMARY KEY (ID))")
	stmt := p.call("mysqli_prepare", link, p.s("INSERT INTO wp_posts (post_title, menu_order, score) VALUES (?, ?, ?)"))
	if types.Deref(p.z, stmt).Type != types.TypeObject {
		t.Fatal("mysqli_prepare n'a pas retourné de mysqli_stmt")
	}
	if n := p.prop(stmt, "param_count").ToLong(); n != 3 {
		t.Fatalf("param_count = %d", n)
	}
	title, _ := types.NewReference(p.z, p.s("Bonjour"))
	order, _ := types.NewReference(p.z, types.NewLong(3))
	score, _ := types.NewReference(p.z, types.NewDouble(1.5))
	if !p.isTrue(p.call("mysqli_stmt_bind_param", stmt, p.s("sid"), title, order, score)) {
		t.Fatal("bind_param a échoué")
	}
	if !p.isTrue(p.call("mysqli_stmt_execute", stmt)) {
		t.Fatalf("execute : %s", p.str(p.call("mysqli_stmt_error", stmt)))
	}
	if id := p.call("mysqli_stmt_insert_id", stmt).ToLong(); id != 1 {
		t.Fatalf("stmt_insert_id = %d", id)
	}
	// Liaison par référence : la deuxième exécution lit les nouvelles valeurs des variables.
	types.AssignRef(p.z, title, p.s("Monde"))
	types.AssignRef(p.z, order, p.s("7"))
	types.AssignRef(p.z, score, types.NewNull())
	p.call("mysqli_stmt_execute", stmt)
	if n := p.call("mysqli_stmt_affected_rows", stmt).ToLong(); n != 1 {
		t.Fatalf("stmt_affected_rows = %d", n)
	}
	arr, _ := types.NewArray(p.z)
	for _, v := range []types.Value{p.s("Tableau"), types.NewLong(9), p.s("2.25")} {
		_ = types.ArrayAppend(p.z, &arr, v)
	}
	if !p.isTrue(p.call("mysqli_stmt_execute", stmt, arr)) {
		t.Fatal("execute avec tableau de paramètres a échoué")
	}
	p.call("mysqli_stmt_close", stmt)

	sel := p.call("mysqli_prepare", link, p.s("SELECT ID, post_title, menu_order, score FROM wp_posts WHERE menu_order >= ? ORDER BY ID"))
	minOrder, _ := types.NewReference(p.z, types.NewLong(4))
	p.call("mysqli_stmt_bind_param", sel, p.s("i"), minOrder)
	p.call("mysqli_stmt_execute", sel)
	res := p.call("mysqli_stmt_get_result", sel)
	if n := p.call("mysqli_num_rows", res).ToLong(); n != 2 {
		t.Fatalf("num_rows = %d", n)
	}
	row := p.call("mysqli_fetch_assoc", res)
	if v := types.Deref(p.z, p.key(row, "ID")); v.Type != types.TypeLong || v.ToLong() != 2 {
		t.Fatal("protocole binaire : ID doit être l'entier 2")
	}
	if p.str(p.key(row, "post_title")) != "Monde" || p.key(row, "menu_order").ToLong() != 7 || types.Deref(p.z, p.key(row, "score")).Type != types.TypeNull {
		t.Fatal("deuxième ligne inattendue")
	}
	row = p.call("mysqli_fetch_assoc", res)
	if v := types.Deref(p.z, p.key(row, "score")); v.Type != types.TypeDouble || v.ToDouble() != 2.25 {
		t.Fatal("score doit être le flottant 2.25")
	}

	// bind_result et fetch écrivent dans les références liées.
	types.AssignRef(p.z, minOrder, types.NewLong(0))
	p.call("mysqli_stmt_execute", sel)
	rid, _ := types.NewReference(p.z, types.NewNull())
	rtitle, _ := types.NewReference(p.z, types.NewNull())
	rorder, _ := types.NewReference(p.z, types.NewNull())
	rscore, _ := types.NewReference(p.z, types.NewNull())
	p.call("mysqli_stmt_bind_result", sel, rid, rtitle, rorder, rscore)
	var titles []string
	for p.isTrue(p.call("mysqli_stmt_fetch", sel)) {
		titles = append(titles, p.cellText(rid)+"="+p.cellText(rtitle))
	}
	if strings.Join(titles, ",") != "int:1=Bonjour,int:2=Monde,int:3=Tableau" {
		t.Fatalf("bind_result = %v", titles)
	}

	bad := p.call("mysqli_prepare", link, p.s("SELECT nope FROM wp_posts WHERE ID = ?"))
	if types.Deref(p.z, bad).Type != types.TypeFalse {
		t.Fatal("préparation d'une colonne inconnue acceptée")
	}
	if n := p.call("mysqli_errno", link).ToLong(); n != 1054 {
		t.Fatalf("errno colonne inconnue = %d", n)
	}
	if _, err := p.callErr("mysqli_stmt_bind_param", sel, p.s("ii"), minOrder); err == nil {
		t.Fatal("bind_param : écart types/variables accepté")
	}
	p.reset()
}

// openDescriptors compte les descripteurs du processus ouverts sous dir.
func openDescriptors(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("/proc/self/fd indisponible")
	}
	n := 0
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err == nil && strings.HasPrefix(target, dir) {
			n++
		}
	}
	return n
}

func TestZoneResetClosesSQLiteHandlesAndRollsBack(t *testing.T) {
	dir := t.TempDir()
	baseHandles := OpenSQLiteHandles()
	p := newPHP(t)
	var links []types.Value
	for i := range 3 {
		links = append(links, p.connectSQLite(filepath.Join(dir, "db"+strconv.Itoa(i)+".sqlite")))
	}
	p.query(links[0], "CREATE TABLE t (id INT NOT NULL AUTO_INCREMENT PRIMARY KEY, v varchar(10))")
	p.query(links[0], "INSERT INTO t (v) VALUES ('validé')")
	p.call("mysqli_begin_transaction", links[0])
	p.query(links[0], "INSERT INTO t (v) VALUES ('perdu')")
	stmt := p.call("mysqli_prepare", links[0], p.s("SELECT v FROM t WHERE id = ?"))
	if types.Deref(p.z, stmt).Type != types.TypeObject {
		t.Fatal("prepare a échoué")
	}
	if fds := openDescriptors(t, dir); fds < 3 {
		t.Fatalf("descripteurs ouverts = %d, au moins 3 attendus", fds)
	}
	if got := OpenSQLiteHandles() - baseHandles; got != 3 {
		t.Fatalf("bases SQLite ouvertes = %d", got)
	}
	if OpenConnCount(p.z) != 3 || OpenStmtCount(p.z) != 1 {
		t.Fatalf("handles avant reset : conn=%d stmt=%d", OpenConnCount(p.z), OpenStmtCount(p.z))
	}
	p.reset()
	if got := OpenSQLiteHandles() - baseHandles; got != 0 {
		t.Fatalf("bases SQLite après reset = %d", got)
	}
	if fds := openDescriptors(t, dir); fds != 0 {
		t.Fatalf("descripteurs restants après reset = %d", fds)
	}
	if OpenConnCount(p.z) != 0 || OpenStmtCount(p.z) != 0 {
		t.Fatal("handles restants après reset")
	}
	// La transaction non validée a été annulée par le nettoyage.
	p2 := newPHP(t)
	l := p2.connectSQLite(filepath.Join(dir, "db0.sqlite"))
	if got := p2.rows(l, "SELECT v FROM t ORDER BY id"); len(got) != 1 || got[0][0] != "validé" {
		t.Fatalf("contenu après reset = %v", got)
	}
	p2.reset()
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	return port
}

func TestTCPConnectionRefused(t *testing.T) {
	t.Setenv(EnvSQLitePath, "")
	t.Setenv(EnvWPSQLite, "")
	port, _ := strconv.Atoi(freePort(t))
	p := newPHP(t)
	p.call("mysqli_report", types.NewLong(ReportOff))
	link := p.call("mysqli_init")
	ok := p.call("mysqli_real_connect", link, p.s("127.0.0.1"), p.s("root"), p.s(""), types.NewNull(), types.NewLong(int64(port)))
	if p.isTrue(ok) {
		t.Fatal("connexion acceptée sur un port fermé")
	}
	if n := p.prop(link, "connect_errno").ToLong(); n != 2002 {
		t.Fatalf("connect_errno = %d", n)
	}
	if msg := p.str(p.call("mysqli_connect_error")); msg != "Connection refused" {
		t.Fatalf("connect_error = %q", msg)
	}
	if v := types.Deref(p.z, p.call("mysqli_connect", p.s("127.0.0.1"), p.s("root"), p.s(""), p.s("db"), types.NewLong(int64(port)))); v.Type != types.TypeFalse {
		t.Fatal("mysqli_connect doit retourner false")
	}
	sock := filepath.Join(t.TempDir(), "absent.sock")
	if p.isTrue(p.call("mysqli_real_connect", link, p.s("localhost"), p.s("root"), p.s(""), types.NewNull(), types.NewNull(), p.s(sock))) {
		t.Fatal("connexion acceptée sur une socket absente")
	}
	if n, msg := p.prop(link, "connect_errno").ToLong(), p.str(p.prop(link, "connect_error")); n != 2002 || msg != "No such file or directory" {
		t.Fatalf("socket absente : %d %q", n, msg)
	}
	if _, err := p.callErr("mysqli_query", link, p.s("SELECT 1")); err == nil || err.Error() != "mysqli object is not fully initialized" {
		t.Fatalf("requête sans connexion : %v", err)
	}

	p.call("mysqli_report", types.NewLong(ReportError|ReportStrict))
	_, err := p.callErr("mysqli_real_connect", link, p.s("127.0.0.1"), p.s("root"), p.s(""), types.NewNull(), types.NewLong(int64(port)))
	var ex SQLException
	if !errors.As(err, &ex) || ex.Code != 2002 || ExceptionClass(err) != "mysqli_sql_exception" {
		t.Fatalf("mode STRICT : %v", err)
	}
	exObj, xerr := NewSQLExceptionObject(p.z, ex)
	if xerr != nil || types.ObjectGet(p.z, exObj, 1).ToLong() != 2002 {
		t.Fatal("objet mysqli_sql_exception inattendu")
	}

	// Repli configuré : aucun serveur ne répond, la base SQLite désignée est ouverte.
	t.Setenv(EnvSQLitePath, filepath.Join(t.TempDir(), "fallback.sqlite"))
	p.call("mysqli_report", types.NewLong(ReportOff))
	if !p.isTrue(p.call("mysqli_real_connect", link, p.s("127.0.0.1"), p.s("root"), p.s(""), p.s("wordpress"), types.NewLong(int64(port)))) {
		t.Fatal("repli SQLite non appliqué")
	}
	if info := p.str(p.prop(link, "server_info")); !strings.HasPrefix(info, "8.0.36") {
		t.Fatalf("server_info = %q", info)
	}
	if v := p.prop(link, "server_version").ToLong(); v != 80036 {
		t.Fatalf("server_version = %d", v)
	}
	p.reset()
}

func TestTranslatorRewrites(t *testing.T) {
	tr := &translator{}
	cases := []struct{ in, want string }{
		{`SELECT 'a\'b', "c""d", 'e\\f'`, `SELECT 'a''b', 'c"d', 'e\f'`},
		{"SELECT * FROM `t` WHERE a = 1 && b = 2 || c <=> NULL", `SELECT * FROM "t" WHERE a = 1  AND  b = 2  OR  c  IS  NULL`},
		{"SELECT NOW(), UNIX_TIMESTAMP(), CONCAT(a, 'x')", `SELECT datetime('now'), CAST(strftime('%s','now') AS INTEGER), ((a) || ('x'))`},
		{"SELECT IF(a > 1, 'y', 'n') FROM t", `SELECT (CASE WHEN a > 1 THEN 'y' ELSE 'n' END) FROM t`},
		{"SELECT a FROM t WHERE b LIKE 'x\\_%'", `SELECT a FROM t WHERE b LIKE 'x\_%' ESCAPE '\'`},
		{"SELECT SQL_CALC_FOUND_ROWS a FROM t FOR UPDATE", `SELECT  a FROM t `},
		{"SELECT CAST(a AS UNSIGNED) FROM t", `SELECT CAST(a AS INTEGER) FROM t`},
	}
	for _, c := range cases {
		if got := tr.expr(lexSQL(c.in)); got != c.want {
			t.Errorf("expr(%q)\n  = %q\nattendu %q", c.in, got, c.want)
		}
	}
	ins, table, upsert := tr.translateInsert(lexSQL("INSERT IGNORE INTO wp_t (a, b) VALUES (1, 'x') ON DUPLICATE KEY UPDATE b = VALUES(b)"))
	if ins != `INSERT OR IGNORE INTO "wp_t" (a, b) VALUES (1, 'x') ON CONFLICT DO UPDATE SET b = excluded."b"` || table != "wp_t" || !upsert {
		t.Errorf("translateInsert = %q", ins)
	}
	rep, _, _ := tr.translateInsert(lexSQL("REPLACE INTO t SET a = 1, b = 'z'"))
	if rep != `INSERT OR REPLACE INTO "t" ("a", "b") VALUES (1, 'z')` {
		t.Errorf("REPLACE ... SET = %q", rep)
	}
	ddl := tr.translateCreateTable(lexSQL(wpOptionsDDL))
	if len(ddl) != 3 || !strings.Contains(ddl[0], `"option_id" INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL /*mysql:bigint(20) unsigned|*/`) ||
		!strings.Contains(ddl[0], `"option_name" varchar(191) NOT NULL DEFAULT '' COLLATE NOCASE /*mysql:varchar(191)|utf8mb4_unicode_520_ci*/`) ||
		ddl[1] != `CREATE UNIQUE INDEX "wp_options__option_name" ON "wp_options" ("option_name")` ||
		ddl[2] != `CREATE INDEX "wp_options__autoload" ON "wp_options" ("autoload")` {
		t.Errorf("CREATE TABLE traduit = %q", ddl)
	}
	if tr.err != nil {
		t.Errorf("erreur de traduction : %v", tr.err)
	}
	if s := substringIndex("www.mysql.com", ".", -2); s != "mysql.com" {
		t.Errorf("SUBSTRING_INDEX négatif = %q", s)
	}
	if !mysqlLike(`\_t%`, "_transient") || mysqlLike(`\_t%`, "xtransient") {
		t.Error("mysqlLike : échappement de _ incorrect")
	}
}

// TestRealMySQLServer exerce le client du protocole contre un serveur MySQL ou MariaDB réel désigné par
// PHP127_MYSQLI_TEST_TCP=hôte:port:utilisateur:mot_de_passe:base ; sans cette variable le test est sauté.
func TestRealMySQLServer(t *testing.T) {
	spec := os.Getenv("PHP127_MYSQLI_TEST_TCP")
	if spec == "" {
		t.Skip("PHP127_MYSQLI_TEST_TCP non défini : aucun serveur MySQL désigné")
	}
	parts := strings.SplitN(spec, ":", 5)
	if len(parts) != 5 {
		t.Fatalf("PHP127_MYSQLI_TEST_TCP mal formé : %q", spec)
	}
	port, _ := strconv.Atoi(parts[1])
	p := newPHP(t)
	p.call("mysqli_report", types.NewLong(ReportOff))
	link := p.call("mysqli_init")
	if !p.isTrue(p.call("mysqli_real_connect", link, p.s(parts[0]), p.s(parts[2]), p.s(parts[3]), p.s(parts[4]), types.NewLong(int64(port)))) {
		t.Fatalf("connexion : %d %s", p.prop(link, "connect_errno").ToLong(), p.cellText(p.prop(link, "connect_error")))
	}
	t.Logf("serveur : %s (%d)", p.str(p.prop(link, "server_info")), p.prop(link, "server_version").ToLong())
	denied := p.call("mysqli_init")
	if p.isTrue(p.call("mysqli_real_connect", denied, p.s(parts[0]), p.s(parts[2]), p.s(parts[3]+"-faux"), p.s(parts[4]), types.NewLong(int64(port)))) {
		t.Fatal("mot de passe erroné accepté")
	}
	if n := p.prop(denied, "connect_errno").ToLong(); n != 1045 {
		t.Fatalf("connect_errno mot de passe erroné = %d", n)
	}
	p.query(link, "DROP TABLE IF EXISTS php127_mysqli_t")
	p.query(link, "CREATE TABLE php127_mysqli_t (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50) NOT NULL, n INT, d DOUBLE, ts DATETIME) DEFAULT CHARSET=utf8mb4")
	esc := p.str(p.call("mysqli_real_escape_string", link, p.s("l'été \"chaud\"")))
	p.query(link, "INSERT INTO php127_mysqli_t (name, n, d, ts) VALUES ('"+esc+"', 42, 1.5, '2024-05-06 07:08:09'), ('b', NULL, -2.25, NULL)")
	if id := p.call("mysqli_insert_id", link).ToLong(); id != 1 {
		t.Fatalf("insert_id = %d", id)
	}
	if n := p.call("mysqli_affected_rows", link).ToLong(); n != 2 {
		t.Fatalf("affected_rows = %d", n)
	}
	got := p.rows(link, "SELECT id, name, n, d, ts FROM php127_mysqli_t ORDER BY id")
	if strings.Join(got[0], "|") != "1|l'été \"chaud\"|42|1.5|2024-05-06 07:08:09" || strings.Join(got[1], "|") != "2|b|<NULL>|-2.25|<NULL>" {
		t.Fatalf("protocole texte = %v", got)
	}
	res := p.query(link, "SELECT id, name FROM php127_mysqli_t")
	f := p.call("mysqli_fetch_field", res)
	if p.str(p.objProp(f, "name")) != "id" || p.objProp(f, "type").ToLong() != typeLongLong || p.objProp(f, "flags").ToLong()&flagAutoIncrement == 0 {
		t.Fatal("métadonnées de champ du serveur inattendues")
	}
	stmt := p.call("mysqli_prepare", link, p.s("SELECT id, name, n, d, ts FROM php127_mysqli_t WHERE id >= ? AND name <> ? ORDER BY id"))
	if types.Deref(p.z, stmt).Type != types.TypeObject {
		t.Fatalf("prepare : %s", p.str(p.call("mysqli_error", link)))
	}
	one, _ := types.NewReference(p.z, types.NewLong(1))
	none, _ := types.NewReference(p.z, p.s("zz"))
	p.call("mysqli_stmt_bind_param", stmt, p.s("is"), one, none)
	if !p.isTrue(p.call("mysqli_stmt_execute", stmt)) {
		t.Fatalf("execute : %s", p.str(p.call("mysqli_stmt_error", stmt)))
	}
	sres := p.call("mysqli_stmt_get_result", stmt)
	row := p.call("mysqli_fetch_row", sres)
	var cells []string
	types.ArrayForEach(p.z, row, func(_, v types.Value) bool {
		cells = append(cells, p.cellText(v))
		return true
	})
	if strings.Join(cells, "|") != "int:1|l'été \"chaud\"|int:42|float:1.5|2024-05-06 07:08:09" {
		t.Fatalf("protocole binaire = %v", cells)
	}
	if !p.isTrue(p.call("mysqli_multi_query", link, p.s("SELECT 1; SELECT 2"))) {
		t.Fatalf("multi_query : %s", p.str(p.call("mysqli_error", link)))
	}
	var seen []string
	for {
		if r := types.Deref(p.z, p.call("mysqli_store_result", link)); r.Type == types.TypeObject {
			seen = append(seen, p.cellText(p.call("mysqli_fetch_column", r)))
		}
		if !p.isTrue(p.call("mysqli_next_result", link)) {
			break
		}
	}
	if strings.Join(seen, ",") != "1,2" {
		t.Fatalf("multi_query = %v", seen)
	}
	if v := types.Deref(p.z, p.call("mysqli_query", link, p.s("SELECT * FROM php127_no_such_table"))); v.Type != types.TypeFalse || p.call("mysqli_errno", link).ToLong() != 1146 {
		t.Fatal("erreur serveur 1146 attendue")
	}
	if !p.isTrue(p.call("mysqli_ping", link)) {
		t.Fatal("ping a échoué")
	}
	p.query(link, "DROP TABLE php127_mysqli_t")
	p.reset()
}

// fakeServer rejoue une suite de paquets serveur, numérotés à partir de 1, derrière un tcpBackend.
func fakeServer(t *testing.T, packets ...[]byte) *tcpBackend {
	t.Helper()
	var buf bytes.Buffer
	for i, p := range packets {
		n := len(p)
		buf.Write([]byte{byte(n), byte(n >> 8), byte(n >> 16), byte(i + 1)})
		buf.Write(p)
	}
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})
	return &tcpBackend{conn: c1, r: bufio.NewReader(&buf)}
}

func colDefPacket(name string, typ uint8, flags uint16) []byte {
	var p []byte
	for _, s := range []string{"def", "", "t", "t", name, name} {
		p = append(p, byte(len(s)))
		p = append(p, s...)
	}
	p = append(p, 0x0c, 0x21, 0x00, 0xff, 0x00, 0x00, 0x00, typ, byte(flags), byte(flags>>8), 0x00, 0x00, 0x00)
	return p
}

var eofPacket = []byte{0xfe, 0x00, 0x00, 0x02, 0x00}

// readResultSafe lit un résultat et convertit une panique en échec de test.
func readResultSafe(t *testing.T, b *tcpBackend, binaryRows bool, label string) (res execResult, serr *sqlError) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s : panique %v", label, r)
			serr = nil
		}
	}()
	res, _, serr = b.readResult(binaryRows)
	return res, serr
}

// Un paquet tronqué ou une longueur annoncée hors du paquet produit l'erreur 2027, jamais une panique
// ni une ligne silencieusement raccourcie.
func TestTruncatedPacketsRejected(t *testing.T) {
	huge := []byte{0xfe, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}
	textCol := colDefPacket("c", typeVarchar, 0)
	cases := []struct {
		name    string
		packets [][]byte
	}{
		{"nombre de colonnes démesuré", [][]byte{huge}},
		{"longueur de cellule démesurée", [][]byte{{1}, textCol, eofPacket, append(slices.Clone(huge), 'a'), eofPacket}},
		{"cellule plus courte que sa longueur", [][]byte{{1}, textCol, eofPacket, {5, 'a', 'b'}, eofPacket}},
		{"entête de longueur tronqué", [][]byte{{1}, textCol, eofPacket, {0xfc, 0x01}, eofPacket}},
		{"définition de colonne tronquée", [][]byte{{1}, {3, 'd', 'e'}, eofPacket, {1, 'a'}, eofPacket}},
		{"paquet de ligne vide", [][]byte{{1}, textCol, eofPacket, {}, eofPacket}},
	}
	for _, c := range cases {
		b := fakeServer(t, c.packets...)
		res, serr := readResultSafe(t, b, false, c.name)
		if serr == nil || serr.errno != 2027 {
			t.Errorf("%s : erreur %v, résultat %+v ; attendu 2027", c.name, serr, res.rs)
		}
	}
}

// Chaque préfixe strict d'une ligne binaire valide est refusé sans panique ; la ligne complète est décodée.
func TestBinaryRowBoundsChecked(t *testing.T) {
	fields := [][]byte{
		colDefPacket("tiny", typeTiny, 0),
		colDefPacket("short", typeShort, 0),
		colDefPacket("long", typeLong, 0),
		colDefPacket("longlong", typeLongLong, 0),
		colDefPacket("float", typeFloat, 0),
		colDefPacket("double", typeDouble, 0),
		colDefPacket("dt", typeDateTime, 0),
		colDefPacket("tm", typeTime, 0),
		colDefPacket("txt", typeVarchar, 0),
	}
	row := []byte{0x00, 0x00, 0x00}
	row = append(row, 0xfe)
	row = binary.LittleEndian.AppendUint16(row, 0xfffe)
	row = binary.LittleEndian.AppendUint32(row, 70000)
	row = binary.LittleEndian.AppendUint64(row, 1<<40)
	row = binary.LittleEndian.AppendUint32(row, math.Float32bits(1.5))
	row = binary.LittleEndian.AppendUint64(row, math.Float64bits(2.25))
	row = append(row, 7)
	row = binary.LittleEndian.AppendUint16(row, 2026)
	row = append(row, 10, 15, 12, 30, 45)
	row = append(row, 8, 0)
	row = binary.LittleEndian.AppendUint32(row, 0)
	row = append(row, 1, 2, 3)
	row = append(row, 3, 'x', 'y', 'z')
	stream := func(r []byte) [][]byte {
		ps := [][]byte{{byte(len(fields))}}
		ps = append(ps, fields...)
		return append(ps, eofPacket, r, eofPacket)
	}
	for n := 0; n < len(row); n++ {
		b := fakeServer(t, stream(row[:n])...)
		_, serr := readResultSafe(t, b, true, "préfixe "+strconv.Itoa(n))
		if serr == nil || serr.errno != 2027 {
			t.Errorf("préfixe de %d octets : erreur %v ; attendu 2027", n, serr)
		}
	}
	b := fakeServer(t, stream(row)...)
	res, serr := readResultSafe(t, b, true, "ligne complète")
	if serr != nil || res.rs == nil || len(res.rs.rows) != 1 {
		t.Fatalf("ligne complète : %v", serr)
	}
	got := res.rs.rows[0]
	if got[0].i != -2 || got[1].i != -2 || got[2].i != 70000 || got[3].i != 1<<40 || got[4].f != 1.5 || got[5].f != 2.25 {
		t.Errorf("valeurs numériques décodées : %+v", got[:6])
	}
	if got[6].s != "2026-10-15 12:30:45" || got[7].s != "01:02:03" || got[8].s != "xyz" {
		t.Errorf("valeurs textuelles décodées : %q %q %q", got[6].s, got[7].s, got[8].s)
	}
}

// Une parenthèse ouvrante sans fermante produit l'erreur de syntaxe 1064, jamais une panique.
func TestUnclosedParenthesisIsSyntaxError(t *testing.T) {
	cases := []string{
		"SELECT CONCAT(", "SELECT IF(", "SELECT CONCAT(a, 'x'", "SELECT (", "SELECT a FROM t WHERE b LIKE (",
		"SELECT a FROM t WHERE b LIKE LOWER(", "CREATE TABLE t (", "CREATE TABLE t (a int", "ALTER TABLE t ADD (a int",
	}
	for _, sql := range cases {
		tr := &translator{}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%q : panique %v", sql, r)
				}
			}()
			toks := lexSQL(sql)
			switch {
			case strings.HasPrefix(sql, "CREATE"):
				tr.translateCreateTable(toks)
			case strings.HasPrefix(sql, "ALTER"):
				tr.translateAlterTable(toks)
			default:
				tr.expr(toks)
			}
		}()
		if tr.err == nil || tr.err.errno != 1064 {
			t.Errorf("%q : erreur %v ; attendu 1064", sql, tr.err)
		}
	}
	p := newPHP(t)
	link := p.connectSQLite(filepath.Join(t.TempDir(), "paren.sqlite"))
	if v := types.Deref(p.z, p.call("mysqli_query", link, p.s("SELECT CONCAT('a', 'b'"))); v.Type != types.TypeFalse {
		t.Fatalf("SELECT CONCAT( non fermé accepté : %v", v)
	}
	if n := p.call("mysqli_errno", link).ToLong(); n != 1064 {
		t.Fatalf("mysqli_errno = %d ; attendu 1064", n)
	}
	p.reset()
}

// Les chaînes lues dans la Zone sont copiées : réécrire l'argument PHP ne modifie ni la valeur rendue
// ni une commande d'initialisation déjà enregistrée.
func TestZoneStringsCopied(t *testing.T) {
	p := newPHP(t)
	v := p.s("SELECT 1")
	got, ok := castString(p.z, v)
	if !ok {
		t.Fatal("castString refusé")
	}
	cmd := p.s("SET NAMES utf8mb4")
	link := p.call("mysqli_init")
	p.call("mysqli_options", link, types.NewLong(optInitCommand), cmd)
	for _, x := range []types.Value{v, cmd} {
		b := types.StringBytes(p.z, x)
		for i := range b {
			b[i] = 'X'
		}
	}
	if got != "SELECT 1" {
		t.Errorf("castString aliasé sur l'arène : %q", got)
	}
	id, _ := objectHandle(p.z, link, ClassIDMysqli)
	if c := getRegistry(p.z).conns[id]; len(c.initCommands) != 1 || c.initCommands[0] != "SET NAMES utf8mb4" {
		t.Errorf("commande d'initialisation aliasée sur l'arène : %q", c.initCommands)
	}
	p.reset()
}

// connect_errno et connect_error suivent PHP 8.3.6 (ext/mysqli/mysqli_prop.c:146-165) : ils lisent l'erreur
// de connexion de la requête, effacée par toute connexion réussie (mysqli_nonapi.c:269), et non l'objet lu.
func TestConnectErrorIsRequestScoped(t *testing.T) {
	t.Setenv(EnvSQLitePath, "")
	t.Setenv(EnvWPSQLite, "")
	port, _ := strconv.Atoi(freePort(t))
	p := newPHP(t)
	p.call("mysqli_report", types.NewLong(ReportOff))
	failed := p.call("mysqli_init")
	p.call("mysqli_real_connect", failed, p.s("127.0.0.1"), p.s("root"), p.s(""), types.NewNull(), types.NewLong(int64(port)))
	if n := p.prop(failed, "connect_errno").ToLong(); n != 2002 {
		t.Fatalf("connect_errno après échec = %d", n)
	}
	p.connectSQLite(filepath.Join(t.TempDir(), "ok.sqlite"))
	if n := p.prop(failed, "connect_errno").ToLong(); n != 0 {
		t.Errorf("connect_errno de l'objet en échec après une connexion réussie = %d ; PHP rend 0", n)
	}
	if v := types.Deref(p.z, p.prop(failed, "connect_error")); v.Type != types.TypeNull {
		t.Errorf("connect_error après une connexion réussie = %v ; PHP rend null", v)
	}
	p.reset()
}

// FOUND_ROWS() après SQL_CALC_FOUND_ROWS compte les lignes de la requête sans LIMIT, y compris avec
// GROUP BY, HAVING, DISTINCT et UNION.
func TestFoundRowsWithGroupingAndUnion(t *testing.T) {
	p := newPHP(t)
	link := p.connectSQLite(filepath.Join(t.TempDir(), "found.sqlite"))
	p.query(link, "CREATE TABLE fr (a int, b int)")
	p.query(link, "INSERT INTO fr (a, b) VALUES (1, 10), (1, 11), (2, 20), (3, 30), (3, 31), (3, 32)")
	cases := []struct {
		sql  string
		want string
	}{
		{"SELECT SQL_CALC_FOUND_ROWS a FROM fr GROUP BY a LIMIT 1", "3"},
		{"SELECT SQL_CALC_FOUND_ROWS a, COUNT(*) FROM fr GROUP BY a HAVING COUNT(*) > 1 LIMIT 1", "2"},
		{"SELECT SQL_CALC_FOUND_ROWS DISTINCT a FROM fr LIMIT 1", "3"},
		{"SELECT SQL_CALC_FOUND_ROWS a FROM fr UNION SELECT b FROM fr LIMIT 2", "9"},
		{"SELECT SQL_CALC_FOUND_ROWS a FROM fr WHERE b > 10 LIMIT 2, 1", "5"},
	}
	for _, c := range cases {
		p.rows(link, c.sql)
		if got := p.rows(link, "SELECT FOUND_ROWS()"); len(got) != 1 || got[0][0] != c.want {
			t.Errorf("%s : FOUND_ROWS() = %v ; attendu %s", c.sql, got, c.want)
		}
	}
	p.reset()
}

// TestUnclosedParenthesisInTypesAndHints couvre les parenthèses non refermées que la traduction refermait
// sans erreur : arguments de type de colonne, DEFAULT, ON UPDATE, colonne générée, liste d'index, USE INDEX.
func TestUnclosedParenthesisInTypesAndHints(t *testing.T) {
	cases := []string{
		"ALTER TABLE t ADD v varchar(10",
		"ALTER TABLE t ADD v int DEFAULT (1",
		"ALTER TABLE t ADD v datetime DEFAULT CURRENT_TIMESTAMP(",
		"ALTER TABLE t ADD v datetime ON UPDATE CURRENT_TIMESTAMP(",
		"ALTER TABLE t ADD v int GENERATED ALWAYS AS (a + 1",
		"ALTER TABLE t ADD INDEX i (a",
		"CREATE TABLE t (a varchar(10) NOT NULL, KEY k (a)",
		"SELECT a FROM t USE INDEX (i",
	}
	for _, sql := range cases {
		tr := &translator{}
		toks := lexSQL(sql)
		switch {
		case strings.HasPrefix(sql, "CREATE"):
			tr.translateCreateTable(toks)
		case strings.HasPrefix(sql, "ALTER"):
			tr.translateAlterTable(toks)
		default:
			tr.expr(toks)
		}
		if tr.err == nil || tr.err.errno != 1064 {
			t.Errorf("%q : erreur %v ; attendu 1064", sql, tr.err)
		}
	}
	p := newPHP(t)
	link := p.connectSQLite(filepath.Join(t.TempDir(), "paren2.sqlite"))
	if v := types.Deref(p.z, p.call("mysqli_query", link, p.s("CREATE TABLE t (a int)"))); v.Type != types.TypeTrue {
		t.Fatalf("CREATE TABLE refusé : %v", v)
	}
	if v := types.Deref(p.z, p.call("mysqli_query", link, p.s("ALTER TABLE t ADD v varchar(10"))); v.Type != types.TypeFalse {
		t.Fatalf("ALTER TABLE t ADD v varchar(10 accepté : %v", v)
	}
	if v := types.Deref(p.z, p.call("mysqli_errno", link)); v.ToLong() != 1064 {
		t.Fatalf("mysqli_errno = %d, attendu 1064", v.ToLong())
	}
}

// TestTruncatedOKAndColumnFixedPartRejected refuse un paquet OK réduit à son marqueur ou privé de ses
// quatre octets d'état, et une définition de colonne privée de sa partie fixe de dix octets.
func TestTruncatedOKAndColumnFixedPartRejected(t *testing.T) {
	full := colDefPacket("c", typeVarchar, 0)
	cases := []struct {
		name    string
		packets [][]byte
	}{
		{"paquet OK réduit à 0x00", [][]byte{{0x00}}},
		{"paquet OK sans état ni avertissements", [][]byte{{0x00, 0x01, 0x00}}},
		{"paquet OK à entier tronqué", [][]byte{{0x00, 0xfc, 0x01}}},
		{"partie fixe de colonne tronquée", [][]byte{{1}, full[:len(full)-3], eofPacket, {1, 'a'}, eofPacket}},
	}
	for _, c := range cases {
		b := fakeServer(t, c.packets...)
		res, serr := readResultSafe(t, b, false, c.name)
		if serr == nil || serr.errno != 2027 {
			t.Errorf("%s : erreur %v, résultat %+v ; attendu 2027", c.name, serr, res)
		}
	}
	b := fakeServer(t, []byte{0x00, 0x02, 0x05, 0x02, 0x00, 0x00, 0x00})
	res, serr := readResultSafe(t, b, false, "paquet OK complet")
	if serr != nil || res.affected != 2 || res.insertID != 5 {
		t.Fatalf("paquet OK complet : erreur %v, résultat %+v", serr, res)
	}
}

// greetingPacket construit le paquet d'accueil du protocole 10 d'un serveur annonçant toutes les capacités.
func greetingPacket() []byte {
	p := []byte{10}
	p = append(p, "8.0.0-php127"...)
	p = append(p, 0)
	p = binary.LittleEndian.AppendUint32(p, 7)
	p = append(p, "abcdefgh"...)
	p = append(p, 0, 0xff, 0xff, 0x21, 0x02, 0x00, 0xff, 0xff, 21)
	p = append(p, make([]byte, 10)...)
	p = append(p, "ijklmnopqrst"...)
	p = append(p, 0)
	p = append(p, "mysql_native_password"...)
	return append(p, 0)
}

// TestClientSSLRefusedBeforeCleartext vérifie qu'une connexion demandant MYSQLI_CLIENT_SSL échoue avec
// l'erreur 2026 avant toute écriture : la réponse de poignée de main en clair n'est jamais émise.
func TestClientSSLRefusedBeforeCleartext(t *testing.T) {
	b := fakeServer(t, greetingPacket())
	done := make(chan *sqlError, 1)
	go func() { done <- b.handshake("root", "secret", "db", 2048) }()
	select {
	case serr := <-done:
		if serr == nil || serr.errno != 2026 {
			t.Fatalf("handshake avec MYSQLI_CLIENT_SSL : %v ; attendu 2026", serr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handshake avec MYSQLI_CLIENT_SSL bloqué sur une écriture en clair")
	}
}

// TestGrantRevokeRefusedOnSQLite vérifie que GRANT et REVOKE échouent sur le moteur SQLite au lieu de réussir sans effet.
func TestGrantRevokeRefusedOnSQLite(t *testing.T) {
	p := newPHP(t)
	link := p.connectSQLite(filepath.Join(t.TempDir(), "grant.sqlite"))
	for _, sql := range []string{"GRANT ALL ON *.* TO 'u'@'%'", "REVOKE ALL ON *.* FROM 'u'@'%'"} {
		if v := types.Deref(p.z, p.call("mysqli_query", link, p.s(sql))); v.Type != types.TypeFalse {
			t.Fatalf("%s accepté : %v", sql, v)
		}
		if v := types.Deref(p.z, p.call("mysqli_errno", link)); v.ToLong() != 1235 {
			t.Fatalf("%s : mysqli_errno = %d, attendu 1235", sql, v.ToLong())
		}
	}
}

// TestWordPressDBHostSQLiteTargets vérifie les couples hôte et socket que wpdb::parse_db_host tire de DB_HOST :
// « sqlite:/chemin » (hôte sqlite), « localhost:/chemin.sqlite » (socket) ; un socket MySQL ordinaire reste MySQL.
func TestWordPressDBHostSQLiteTargets(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		host, socket string
		want         string
		ok           bool
	}{
		{"sqlite", filepath.Join(dir, "a.sqlite"), filepath.Join(dir, "a.sqlite"), true},
		{"SQLite", "", ":memory:", true},
		{"localhost", filepath.Join(dir, "b.sqlite"), filepath.Join(dir, "b.sqlite"), true},
		{"localhost", filepath.Join(dir, "c.db"), filepath.Join(dir, "c.db"), true},
		{"localhost", "/var/run/mysqld/mysqld.sock", "", false},
		{"localhost", "", "", false},
	}
	for _, c := range cases {
		got, ok := sqliteHostSocket(c.host, c.socket)
		if ok != c.ok || got != c.want {
			t.Errorf("sqliteHostSocket(%q, %q) = %q, %v ; attendu %q, %v", c.host, c.socket, got, ok, c.want, c.ok)
		}
	}
	p := newPHP(t)
	p.call("mysqli_report", types.NewLong(ReportOff))
	link := p.call("mysqli_init")
	path := filepath.Join(dir, "wp.sqlite")
	ok := p.call("mysqli_real_connect", link, p.s("localhost"), p.s("root"), p.s(""), types.NewNull(), types.NewNull(), p.s(path), types.NewLong(0))
	if !p.isTrue(ok) {
		t.Fatalf("connexion localhost + socket SQLite refusée : %s", p.cellText(p.prop(link, "connect_error")))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("base SQLite non créée au chemin du socket : %v", err)
	}
}

// greetingServer sert sur 127.0.0.1 un unique paquet de bienvenue de contenu payload, lit au plus un paquet de
// réponse puis ferme la connexion ; l'adresse d'écoute est rendue.
func greetingServer(t testing.TB, payload []byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		n := len(payload)
		if _, err := c.Write(append([]byte{byte(n), byte(n >> 8), byte(n >> 16), 0}, payload...)); err != nil {
			return
		}
		var hdr [4]byte
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			return
		}
		_, _ = io.CopyN(io.Discard, c, int64(uint32(hdr[0])|uint32(hdr[1])<<8|uint32(hdr[2])<<16))
	}()
	return ln.Addr().String()
}

// dialGreeting ouvre une session contre greetingServer et convertit une panique en échec de test.
func dialGreeting(t testing.TB, payload []byte) (serr *sqlError) {
	t.Helper()
	addr := greetingServer(t, payload)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("poignée de main sur %x : panique %v", payload, r)
			serr = nil
		}
	}()
	b, serr := dialMySQL("tcp", addr, "127.0.0.1", "root", "secret", "db", 0, 5*time.Second, 5*time.Second)
	if b != nil {
		b.close()
	}
	return serr
}

// TestHandshakeTruncated13And14Bytes envoie un paquet de bienvenue dont la partie qui suit la version
// « 5.7.0 » compte 13 puis 14 octets : la lecture de l'identifiant (4), du nonce et de son NUL (9) et des
// capacités (2) exige 15 octets, l'erreur attendue est 2027 et jamais une panique. À 15 octets la garde est
// franchie et l'échec vient de la fermeture par le serveur, pas du paquet de bienvenue.
func TestHandshakeTruncated13And14Bytes(t *testing.T) {
	for _, extra := range []int{13, 14, 15} {
		payload := append([]byte{10}, "5.7.0\x00"...)
		payload = append(payload, bytes.Repeat([]byte{0x41}, extra)...)
		serr := dialGreeting(t, payload)
		switch {
		case extra < 15 && (serr == nil || serr.errno != 2027 || !strings.Contains(serr.msg, "Malformed packet")):
			t.Errorf("%d octets après la version : erreur %v ; attendu 2027 Malformed packet", extra, serr)
		case extra == 15 && (serr == nil || serr.errno == 2027):
			t.Errorf("15 octets après la version : erreur %v ; attendu une erreur de connexion autre que 2027", serr)
		}
	}
}

// sqliteErrno exécute sql par mysqli_query et rend mysqli_errno, ou 0 si la requête réussit.
func (p *php) sqliteErrno(link types.Value, sql string) int64 {
	p.t.Helper()
	if v := types.Deref(p.z, p.call("mysqli_query", link, p.s(sql))); v.Type != types.TypeFalse {
		return 0
	}
	return p.call("mysqli_errno", link).ToLong()
}

// TestSQLiteSecurityBlocksPragmaAttachVacuum vérifie que les verbes propres à SQLite (PRAGMA, ATTACH,
// DETACH, VACUUM, REINDEX) et tout verbe hors de la liste blanche MySQL échouent par 1064, à l'exécution
// comme à la préparation, sans créer de fichier, et que SHOW TABLES, SHOW COLUMNS et DESCRIBE, qui lisent
// les pragmas d'introspection en interne, fonctionnent encore.
func TestSQLiteSecurityBlocksPragmaAttachVacuum(t *testing.T) {
	dir := t.TempDir()
	p := newPHP(t)
	link := p.connectSQLite(filepath.Join(dir, "sec.sqlite"))
	p.query(link, "CREATE TABLE t (id INT NOT NULL, v VARCHAR(10), PRIMARY KEY (id))")
	target := filepath.Join(dir, "x.db")
	refused := []string{
		"PRAGMA writable_schema = 1",
		"PRAGMA table_info(t)",
		"ATTACH DATABASE '" + target + "' AS e",
		"VACUUM INTO '" + target + "'",
		"  /* tête précédée d'un commentaire */ pragma writable_schema = ON",
		"DETACH DATABASE main",
		"REINDEX t",
		"VACUUM",
		"FOO BAR",
		"`SELECT` 1",
	}
	for _, sql := range refused {
		if n := p.sqliteErrno(link, sql); n != 1064 {
			t.Errorf("mysqli_query(%q) : errno %d ; attendu 1064", sql, n)
		}
		if v := types.Deref(p.z, p.call("mysqli_prepare", link, p.s(sql))); v.Type != types.TypeFalse {
			t.Errorf("mysqli_prepare(%q) accepté", sql)
		} else if n := p.call("mysqli_errno", link).ToLong(); n != 1064 {
			t.Errorf("mysqli_prepare(%q) : errno %d ; attendu 1064", sql, n)
		}
	}
	if v := types.Deref(p.z, p.call("mysqli_multi_query", link, p.s("SELECT 1; ATTACH DATABASE '"+target+"' AS e"))); v.Type != types.TypeFalse {
		for p.isTrue(p.call("mysqli_more_results", link)) && p.isTrue(p.call("mysqli_next_result", link)) {
		}
	}
	if n := p.call("mysqli_errno", link).ToLong(); n != 1064 {
		t.Errorf("mysqli_multi_query avec ATTACH en seconde instruction : errno %d ; attendu 1064", n)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fichier %s créé ou inaccessible : %v", target, err)
	}
	if rows := p.rows(link, "SHOW TABLES"); len(rows) != 1 || rows[0][0] != "t" {
		t.Errorf("SHOW TABLES = %q", rows)
	}
	if rows := p.rows(link, "SHOW COLUMNS FROM t"); len(rows) != 2 || rows[0][0] != "id" || rows[1][0] != "v" {
		t.Errorf("SHOW COLUMNS FROM t = %q", rows)
	}
	if rows := p.rows(link, "DESCRIBE t"); len(rows) != 2 || rows[0][3] != "PRI" {
		t.Errorf("DESCRIBE t = %q", rows)
	}
	p.reset()
}

// TestSQLiteAuthorizerAndDefensiveMode vérifie la seconde ligne de défense, sous la traduction : sur une
// instruction transmise telle quelle à SQLite, l'autorisateur refuse ATTACH et les pragmas hors
// introspection, le mode défensif est actif et un nom « file:...?mode=memory » reste un nom de fichier.
func TestSQLiteAuthorizerAndDefensiveMode(t *testing.T) {
	dir := t.TempDir()
	b, serr := openSQLiteBackend(filepath.Join(dir, "auth.sqlite"), "db")
	if serr != nil {
		t.Fatal(serr)
	}
	defer b.close()
	if on, rc := b.db.configFlag(sqliteConfigDefensive, -1); rc != sqliteOK || !on {
		t.Fatalf("SQLITE_DBCONFIG_DEFENSIVE = %v (rc %d) ; attendu actif", on, rc)
	}
	if _, _, err := b.run("CREATE TABLE t (id INTEGER, v TEXT)", nil); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "x.db")
	for _, sql := range []string{
		"ATTACH DATABASE '" + target + "' AS e",
		"VACUUM INTO '" + target + "'",
		"PRAGMA writable_schema = 1",
		"PRAGMA journal_mode = OFF",
		"PRAGMA schema_version = 99",
		"SELECT * FROM pragma_compile_options",
		"SELECT load_extension('" + filepath.Join(dir, "absent.so") + "')",
	} {
		// SQLite libelle un refus « not authorized » ou « authorization denied » selon l'étape qui l'oppose.
		if _, _, err := b.run(sql, nil); err == nil || !(strings.Contains(err.msg, "not authorized") || strings.Contains(err.msg, "authorization denied")) {
			t.Errorf("%q : erreur %v ; attendu un refus d'autorisation", sql, err)
		}
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fichier %s créé ou inaccessible : %v", target, err)
	}
	for _, sql := range []string{`PRAGMA table_info("t")`, `PRAGMA index_list("t")`, `PRAGMA foreign_key_list("t")`, "PRAGMA database_list"} {
		if _, _, err := b.run(sql, nil); err != nil {
			t.Errorf("pragma d'introspection %q refusé : %v", sql, err)
		}
	}

	t.Chdir(dir)
	name := "file:uri.db?mode=memory"
	db, rc, msg := sqliteOpen(name)
	if rc != sqliteOK {
		t.Fatalf("sqliteOpen(%q) : %d %s", name, rc, msg)
	}
	defer db.close()
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatalf("%q interprété comme URI : aucun fichier de ce nom (%v)", name, err)
	}
}

// TestForeignKeyChecksWithoutPragma vérifie que SET foreign_key_checks, appliqué par
// SQLITE_DBCONFIG_ENABLE_FKEY, active et désactive encore les clés étrangères.
func TestForeignKeyChecksWithoutPragma(t *testing.T) {
	p := newPHP(t)
	link := p.connectSQLite(filepath.Join(t.TempDir(), "fk.sqlite"))
	p.query(link, "CREATE TABLE parent (id INT NOT NULL, PRIMARY KEY (id))")
	p.query(link, "CREATE TABLE child (id INT NOT NULL, pid INT, PRIMARY KEY (id), FOREIGN KEY (pid) REFERENCES parent (id))")
	if n := p.sqliteErrno(link, "INSERT INTO child VALUES (1, 42)"); n != 1452 {
		t.Fatalf("clé étrangère violée avec foreign_key_checks = 1 : errno %d ; attendu 1452", n)
	}
	p.query(link, "SET foreign_key_checks = 0")
	p.query(link, "INSERT INTO child VALUES (2, 43)")
	p.query(link, "SET foreign_key_checks = 1")
	if n := p.sqliteErrno(link, "INSERT INTO child VALUES (3, 44)"); n != 1452 {
		t.Fatalf("clé étrangère violée après rétablissement : errno %d ; attendu 1452", n)
	}
	p.reset()
}

// TestSQLiteMetadataCachesInvalidatedByDDL vérifie que hasAutoIncrement et buildFields lisent leurs caches,
// et qu'une table recréée sans AUTO_INCREMENT, ou une création annulée, ne conserve aucune entrée périmée.
func TestSQLiteMetadataCachesInvalidatedByDDL(t *testing.T) {
	b, serr := openSQLiteBackend(filepath.Join(t.TempDir(), "cache.sqlite"), "db")
	if serr != nil {
		t.Fatal(serr)
	}
	defer b.close()
	q := func(sql string) execResult {
		t.Helper()
		res, err := b.query(sql, false)
		if err != nil {
			t.Fatalf("%s : %v", sql, err)
		}
		return res[0]
	}
	q("CREATE TABLE t (id BIGINT NOT NULL AUTO_INCREMENT, v VARCHAR(10), PRIMARY KEY (id))")
	if r := q("INSERT INTO t (v) VALUES ('a')"); r.insertID != 1 {
		t.Fatalf("insert_id = %d ; attendu 1", r.insertID)
	}
	if v, ok := b.autoIncCache["t"]; !ok || !v {
		t.Fatalf("autoIncCache[t] = %v, %v ; attendu true, présent", v, ok)
	}
	if r := q("INSERT INTO T (v) VALUES ('b')"); r.insertID != 2 {
		t.Fatalf("insert_id par nom de table en capitales = %d ; attendu 2", r.insertID)
	}
	r := q("SELECT id, v FROM t")
	if r.rs.fields[0].flags&flagAutoIncrement == 0 {
		t.Fatalf("drapeaux de id = %#x ; AUTO_INCREMENT attendu", r.rs.fields[0].flags)
	}
	if m, ok := b.colMetaCache["t"]["id"]; !ok || !m.autoInc || !m.ok || !strings.EqualFold(m.decltype, "INTEGER") {
		t.Fatalf("colMetaCache[t][id] = %+v, %v", m, ok)
	}
	// Une entrée faussée à dessein doit se lire dans le résultat : le cache est consulté avant SQLite.
	b.autoIncCache["t"] = false
	b.colMetaCache["t"]["id"] = colMeta{decltype: "INTEGER", ok: true}
	if r := q("INSERT INTO t (v) VALUES ('p')"); r.insertID != 0 {
		t.Fatalf("insert_id = %d malgré autoIncCache[t] = false : cache non consulté", r.insertID)
	}
	if r := q("SELECT id FROM t"); r.rs.fields[0].flags&flagAutoIncrement != 0 {
		t.Fatalf("drapeau AUTO_INCREMENT malgré colMetaCache[t][id] sans autoInc : cache non consulté")
	}
	q("DROP TABLE t")
	if len(b.autoIncCache) != 0 || len(b.colMetaCache) != 0 {
		t.Fatalf("caches non vidés par DROP TABLE : %v %v", b.autoIncCache, b.colMetaCache)
	}
	q("CREATE TABLE t (id BIGINT NOT NULL, v VARCHAR(10), PRIMARY KEY (id))")
	if r := q("INSERT INTO t (id, v) VALUES (7, 'c')"); r.insertID != 0 {
		t.Fatalf("insert_id sur table sans AUTO_INCREMENT = %d ; attendu 0", r.insertID)
	}
	if r := q("SELECT id FROM t"); r.rs.fields[0].flags&flagAutoIncrement != 0 {
		t.Fatalf("drapeau AUTO_INCREMENT périmé sur id : %#x", r.rs.fields[0].flags)
	}
	q("BEGIN")
	q("CREATE TABLE u (id BIGINT NOT NULL AUTO_INCREMENT, PRIMARY KEY (id))")
	q("INSERT INTO u (id) VALUES (NULL)")
	if !b.autoIncCache["u"] {
		t.Fatal("autoIncCache[u] non rempli")
	}
	q("ROLLBACK")
	if _, ok := b.autoIncCache["u"]; ok {
		t.Fatal("autoIncCache[u] conservé après ROLLBACK de la création")
	}
}

// TestSQLiteFetchRowBatch vérifie le décodage de p127_fetch_row pour chaque type SQLite, y compris un
// texte à octet NUL interne, une chaîne vide et un blob, puis sur mille lignes.
func TestSQLiteFetchRowBatch(t *testing.T) {
	b, serr := openSQLiteBackend(filepath.Join(t.TempDir(), "fetch.sqlite"), "db")
	if serr != nil {
		t.Fatal(serr)
	}
	defer b.close()
	_, rows, err := b.run("SELECT 1, 2.5, 'abc', X'00ff', NULL, '', CAST(X'610062' AS TEXT), 9223372036854775807, -0.0, X''", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []cell{
		{kind: cellInt, i: 1}, {kind: cellFloat, f: 2.5}, {kind: cellText, s: "abc"}, {kind: cellBlob, s: "\x00\xff"},
		{kind: cellNull}, {kind: cellText, s: ""}, {kind: cellText, s: "a\x00b"}, {kind: cellInt, i: math.MaxInt64},
		{kind: cellFloat, f: 0}, {kind: cellBlob, s: ""},
	}
	if len(rows) != 1 || len(rows[0]) != len(want) {
		t.Fatalf("lignes = %+v", rows)
	}
	for i, w := range want {
		if g := rows[0][i]; g.kind != w.kind || g.i != w.i || g.f != w.f || g.s != w.s {
			t.Errorf("colonne %d = %+v ; attendu %+v", i, g, w)
		}
	}
	_, rows, err = b.run("WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 1000) SELECT x, 'v' || x, x * 0.5 FROM c", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1000 {
		t.Fatalf("%d lignes ; attendu 1000", len(rows))
	}
	for k, r := range rows {
		x := int64(k + 1)
		if r[0].kind != cellInt || r[0].i != x || r[1].s != "v"+strconv.FormatInt(x, 10) || r[2].f != float64(x)*0.5 {
			t.Fatalf("ligne %d = %+v", k, r)
		}
	}
}

// escapeStringReference est la définition octet par octet de mysql_real_escape_string, contre laquelle
// escapeString, qui copie par tranches entre deux positions rendues par C2_sql_find_escape, est confronté.
func escapeStringReference(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case 0:
			b.WriteString(`\0`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		case '"':
			b.WriteString(`\"`)
		case 0x1a:
			b.WriteString(`\Z`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// sqlScanInputs construit les entrées de parité : requêtes SQL, plages d'espaces, de mots et d'octets sains
// rompues à chaque position par un octet de chaque classe, octets aléatoires.
func sqlScanInputs() [][]byte {
	var out [][]byte
	for _, s := range []string{
		"", " ", "SELECT 1", "  \t\n\r\v\fSELECT", "select `a`, b$c, _d FROM t WHERE x = 'y\\'z' AND w = \"q\"",
		"INSERT INTO wp_options (option_name) VALUES ('a\x00b\x1a\r\n')", "caf\xc3\xa9_col1 = 2",
		strings.Repeat(" ", 100) + "x", strings.Repeat("abc_$09XYZ", 20) + "-", strings.Repeat("safe text ", 40) + "'",
	} {
		out = append(out, []byte(s))
	}
	breakers := []byte{0x00, '\t', '\n', '\v', '\f', '\r', 0x08, 0x0e, 0x1a, 0x1f, ' ', '!', '"', '$', '\'', '-', '/', '0', '9', ':',
		'@', 'A', 'Z', '[', '\\', '_', '`', 'a', 'z', '{', 0x7f, 0x80, 0xff}
	for _, fill := range []byte{' ', '\t', 'a', 'Q', '5', '_', '$', 0x80, 0xc3, 'x', '.', '%'} {
		for _, n := range []int{31, 32, 33, 63, 64, 65, 96, 100} {
			for _, br := range breakers {
				for _, k := range []int{0, 1, 15, 16, 30, 31, 32, 33, 47, 63, 64, 70, 99} {
					if k >= n {
						continue
					}
					b := bytes.Repeat([]byte{fill}, n)
					b[k] = br
					out = append(out, b)
				}
			}
		}
	}
	rng := rand.New(rand.NewPCG(127, 82))
	for n := range 160 {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(rng.Uint32())
		}
		out = append(out, b)
	}
	return out
}

// TestSgoiterSQLScanParityVsGCC compile c_src/sql_scan.c par gcc -O2 avec son oracle et vérifie, bit à bit,
// que les entrées C2_sql_skip_spaces, C2_sql_scan_word et C2_sql_find_escape émises par sgoiter rendent
// la même position que les marches scalaire, AVX2 et publique du C, puis qu'escapeString et la définition
// octet par octet concordent.
func TestSgoiterSQLScanParityVsGCC(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "sql_scan_oracle")
	cmd := exec.CommandContext(t.Context(), "gcc", "-O2", "-Wall", "-Wextra", "-Werror",
		"c_src/sql_scan_oracle.c", "c_src/sql_scan.c", "-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	type scanCase struct {
		src      []byte
		pos, end uint64
	}
	var cases []scanCase
	var in bytes.Buffer
	for _, src := range sqlScanInputs() {
		n := uint64(len(src))
		bounds := [][2]uint64{{0, n}}
		if n > 0 {
			bounds = append(bounds, [2]uint64{1, n}, [2]uint64{n / 2, n}, [2]uint64{0, n - 1}, [2]uint64{n, n})
		}
		if n > 40 {
			bounds = append(bounds, [2]uint64{3, n - 5}, [2]uint64{n - 33, n})
		}
		for _, be := range bounds {
			cases = append(cases, scanCase{src, be[0], be[1]})
			h := "-"
			if n > 0 {
				h = hex.EncodeToString(src)
			}
			fmt.Fprintf(&in, "%s %d %d\n", h, be[0], be[1])
		}
	}
	run := exec.CommandContext(t.Context(), bin)
	run.Stdin = &in
	var stderr bytes.Buffer
	run.Stderr = &stderr
	out, err := run.Output()
	if err != nil {
		t.Fatalf("oracle : %v\n%s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("%d lignes d'oracle pour %d cas", len(lines), len(cases))
	}
	kernels := []struct {
		name   string
		public func([]byte, uint64, uint64) uint64
		scalar func([]byte, uint64, uint64) uint64
	}{
		{"skip_spaces", C2_sql_skip_spaces, C2_sql_skip_spaces_scalar},
		{"scan_word", C2_sql_scan_word, C2_sql_scan_word_scalar},
		{"find_escape", C2_sql_find_escape, C2_sql_find_escape_scalar},
	}
	for ci, c := range cases {
		f := strings.Fields(lines[ci])
		if len(f) != 9 {
			t.Fatalf("ligne d'oracle %q", lines[ci])
		}
		for ki, k := range kernels {
			var want [3]uint64
			for j := range want {
				v, err := strconv.ParseUint(f[3*ki+j], 10, 64)
				if err != nil {
					t.Fatal(err)
				}
				want[j] = v
			}
			if want[0] != want[1] || want[0] != want[2] {
				t.Fatalf("%s : marches C divergentes %v sur %x [%d:%d]", k.name, want, c.src, c.pos, c.end)
			}
			if got := k.public(c.src, c.pos, c.end); got != want[0] {
				t.Fatalf("C2_sql_%s(%x, %d, %d) = %d ; gcc -O2 rend %d", k.name, c.src, c.pos, c.end, got, want[0])
			}
			if got := k.scalar(c.src, c.pos, c.end); got != want[0] {
				t.Fatalf("C2_sql_%s_scalar(%x, %d, %d) = %d ; gcc -O2 rend %d", k.name, c.src, c.pos, c.end, got, want[0])
			}
		}
		if c.pos == 0 && c.end == uint64(len(c.src)) {
			if got, want := escapeString(string(c.src)), escapeStringReference(string(c.src)); got != want {
				t.Fatalf("escapeString(%x) = %x ; attendu %x", c.src, got, want)
			}
		}
	}
	t.Logf("%d cas confrontés à gcc -O2", len(cases))
	// regen réémet les deux variantes depuis c_src/sql_scan.c dans un répertoire temporaire et les compare
	// octet par octet aux fichiers du dépôt : une retouche manuelle ou une source C modifiée sans
	// régénération échoue, de même que l'absence du transpileur.
	t.Run("regen", func(t *testing.T) {
		const sgoiter = "/devhoros/c2simd/bin/sgoiter"
		src, err := filepath.Abs("c_src/sql_scan.c")
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		cmd := exec.CommandContext(t.Context(), sgoiter, "-in", src, "-out", filepath.Join(dir, "sql_scan_gen.go"), "-pkg", "mysqli")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sgoiter : %v\n%s", err, out)
		}
		for _, name := range []string{"sql_scan_gen.go", "sql_scan_nosimd_gen.go"} {
			want, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s diffère de la sortie de sgoiter sur c_src/sql_scan.c", name)
			}
		}
	})
}

// authServer sert sur 127.0.0.1 le paquet de bienvenue de greetingPacket, lit la réponse du client puis émet
// reply comme paquet de conclusion de l'authentification (numéro de séquence 2) ; l'adresse d'écoute est rendue.
func authServer(t testing.TB, reply []byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		frame := func(seq byte, p []byte) []byte {
			n := len(p)
			return append([]byte{byte(n), byte(n >> 8), byte(n >> 16), seq}, p...)
		}
		if _, err := c.Write(frame(0, greetingPacket())); err != nil {
			return
		}
		var hdr [4]byte
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			return
		}
		if _, err := io.CopyN(io.Discard, c, int64(uint32(hdr[0])|uint32(hdr[1])<<8|uint32(hdr[2])<<16)); err != nil {
			return
		}
		if _, err := c.Write(frame(2, reply)); err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, c)
	}()
	return ln.Addr().String()
}

// TestTruncatedAuthOKAndTruncatedEOFRejected vérifie qu'un paquet OK de fin d'authentification privé de ses
// entiers ou de ses quatre octets d'état, et qu'un paquet EOF de fin de colonnes ou de fin de lignes réduit
// à moins de cinq octets, échouent par 2027 au lieu de conclure au succès.
func TestTruncatedAuthOKAndTruncatedEOFRejected(t *testing.T) {
	for _, reply := range [][]byte{{0x00}, {0x00, 0x00, 0x00}, {0x00, 0x00, 0x00, 0x02, 0x00, 0x00}} {
		b, serr := dialMySQL("tcp", authServer(t, reply), "127.0.0.1", "root", "secret", "db", 0, 5*time.Second, 5*time.Second)
		if b != nil {
			b.close()
		}
		if serr == nil || serr.errno != 2027 {
			t.Errorf("paquet OK d'authentification %x : erreur %v ; attendu 2027", reply, serr)
		}
	}
	b, serr := dialMySQL("tcp", authServer(t, []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00}), "127.0.0.1", "root", "secret", "db", 0, 5*time.Second, 5*time.Second)
	if serr != nil || b == nil {
		t.Fatalf("paquet OK d'authentification complet refusé : %v", serr)
	}
	b.close()

	textCol := colDefPacket("c", typeVarchar, 0)
	for _, n := range []int{1, 2, 4} {
		short := eofPacket[:n]
		for _, c := range []struct {
			name    string
			packets [][]byte
		}{
			{"fin de colonnes", [][]byte{{1}, textCol, short, {1, 'a'}, eofPacket}},
			{"fin de lignes", [][]byte{{1}, textCol, eofPacket, {1, 'a'}, short}},
		} {
			label := fmt.Sprintf("%s en paquet EOF de %d octets", c.name, n)
			fb := fakeServer(t, c.packets...)
			res, serr := readResultSafe(t, fb, false, label)
			if serr == nil || serr.errno != 2027 {
				t.Errorf("%s : erreur %v, résultat %+v ; attendu 2027", label, serr, res)
			}
			// Le flux de paquets n'est plus synchronisé : la connexion doit être fermée.
			if fb.conn != nil {
				t.Errorf("%s : connexion laissée ouverte après le paquet mal formé", label)
			}
		}
	}
	res, serr := readResultSafe(t, fakeServer(t, []byte{1}, textCol, eofPacket, []byte{1, 'a'}, eofPacket), false, "résultat complet")
	if serr != nil || res.rs == nil || len(res.rs.rows) != 1 {
		t.Fatalf("résultat à paquets EOF complets : erreur %v, résultat %+v", serr, res)
	}
}

// TestAlterTableModifyAndChangeColumnAppliesSchema vérifie que MODIFY et CHANGE appliquent le type, la
// nullabilité et la valeur par défaut demandés, observés par DESCRIBE, SHOW COLUMNS et une insertion, en
// conservant les lignes, les index et le compteur AUTO_INCREMENT ; un passage en NOT NULL sur une colonne qui
// contient NULL échoue sans altérer la table.
func TestAlterTableModifyAndChangeColumnAppliesSchema(t *testing.T) {
	p := newPHP(t)
	link := p.connectSQLite(filepath.Join(t.TempDir(), "alter.sqlite"))
	p.query(link, "CREATE TABLE items (id BIGINT NOT NULL AUTO_INCREMENT, title VARCHAR(20), price INT, note TEXT, PRIMARY KEY (id), KEY title (title))")
	p.query(link, "INSERT INTO items (title, price, note) VALUES ('a', 3, 'x'), ('b', 4, NULL), ('c', 5, 'z')")
	p.query(link, "DELETE FROM items WHERE id = 3")

	p.query(link, "ALTER TABLE items MODIFY COLUMN price DECIMAL(10,2) NOT NULL DEFAULT 5.00")
	desc := p.rows(link, "DESCRIBE items")
	if len(desc) != 4 || desc[2][0] != "price" || desc[2][1] != "decimal(10,2)" || desc[2][2] != "NO" || desc[2][4] != "5.00" {
		t.Fatalf("DESCRIBE items après MODIFY = %q", desc)
	}
	p.query(link, "INSERT INTO items (title) VALUES ('d')")
	if rows := p.rows(link, "SELECT id, price FROM items WHERE title = 'd'"); len(rows) != 1 || rows[0][0] != "4" || rows[0][1] != "5" {
		t.Fatalf("ligne insérée après MODIFY = %q ; attendu id 4 (compteur conservé) et price 5", rows)
	}
	if n := p.sqliteErrno(link, "INSERT INTO items (title, price) VALUES ('e', NULL)"); n != 1048 {
		t.Fatalf("price NULL après MODIFY ... NOT NULL : errno %d ; attendu 1048", n)
	}

	p.query(link, "ALTER TABLE items CHANGE COLUMN title label VARCHAR(64) NOT NULL DEFAULT 'n/a'")
	cols := p.rows(link, "SHOW COLUMNS FROM items")
	if len(cols) != 4 || cols[1][0] != "label" || cols[1][1] != "varchar(64)" || cols[1][2] != "NO" || cols[1][3] != "MUL" || cols[1][4] != "n/a" {
		t.Fatalf("SHOW COLUMNS FROM items après CHANGE = %q", cols)
	}
	if rows := p.rows(link, "SELECT id, label, price, note FROM items ORDER BY id"); !slices.EqualFunc(rows, [][]string{
		{"1", "a", "3", "x"}, {"2", "b", "4", "<NULL>"}, {"4", "d", "5", "<NULL>"},
	}, slices.Equal) {
		t.Fatalf("lignes après CHANGE = %q", rows)
	}
	p.query(link, "INSERT INTO items (price) VALUES (7)")
	if rows := p.rows(link, "SELECT label FROM items WHERE price = 7"); len(rows) != 1 || rows[0][0] != "n/a" {
		t.Fatalf("valeur par défaut de label = %q ; attendu n/a", rows)
	}
	if idx := p.rows(link, "SHOW INDEX FROM items"); !slices.ContainsFunc(idx, func(r []string) bool { return r[2] == "title" && r[4] == "label" }) {
		t.Fatalf("index title perdu ou non renommé : %q", idx)
	}

	before := p.rows(link, "SELECT id, label, price, note FROM items ORDER BY id")
	if n := p.sqliteErrno(link, "ALTER TABLE items MODIFY note TEXT NOT NULL"); n != 1138 {
		t.Fatalf("MODIFY note NOT NULL sur des lignes NULL : errno %d ; attendu 1138", n)
	}
	if after := p.rows(link, "SELECT id, label, price, note FROM items ORDER BY id"); !slices.EqualFunc(before, after, slices.Equal) {
		t.Fatalf("table altérée par un MODIFY refusé : %q ; attendu %q", after, before)
	}
	if desc := p.rows(link, "DESCRIBE items"); desc[3][0] != "note" || desc[3][2] != "YES" {
		t.Fatalf("note après MODIFY refusé = %q", desc[3])
	}
	if tables := p.rows(link, "SHOW TABLES"); len(tables) != 1 || tables[0][0] != "items" {
		t.Fatalf("SHOW TABLES après MODIFY refusé = %q", tables)
	}
	p.reset()
}

// TestLockTablesEnforcesLockedSetAndTransaction vérifie que LOCK TABLES ouvre une transaction SQLite tenue
// jusqu'à UNLOCK TABLES, qu'une table hors de l'ensemble verrouillé est refusée par 1100 et qu'une syntaxe
// vide ou invalide est refusée par 1064.
func TestLockTablesEnforcesLockedSetAndTransaction(t *testing.T) {
	p := newPHP(t)
	link := p.connectSQLite(filepath.Join(t.TempDir(), "lock.sqlite"))
	p.query(link, "CREATE TABLE t1 (id INT NOT NULL, v VARCHAR(10), PRIMARY KEY (id))")
	p.query(link, "CREATE TABLE t2 (id INT NOT NULL, PRIMARY KEY (id))")
	p.query(link, "INSERT INTO t2 VALUES (1)")
	for _, sql := range []string{"LOCK TABLES", "LOCK TABLES t1", "LOCK TABLES t1 SHARE", "LOCK TABLES t1 WRITE,", "LOCK TABLES t1 READ t2 WRITE"} {
		if n := p.sqliteErrno(link, sql); n != 1064 {
			t.Errorf("%q : errno %d ; attendu 1064", sql, n)
		}
	}
	// La requête LOCK TABLES est empoisonnée dans l'arène après exécution : l'ensemble verrouillé n'en
	// conserve aucune vue (pilier 1).
	lockSQL := p.s("LOCK TABLES t1 WRITE")
	if v := types.Deref(p.z, p.call("mysqli_query", link, lockSQL)); v.Type != types.TypeTrue {
		t.Fatalf("LOCK TABLES t1 WRITE refusé : %d", p.call("mysqli_errno", link).ToLong())
	}
	for b := types.StringBytes(p.z, lockSQL); len(b) > 0; b = b[1:] {
		b[0] = 'X'
	}
	p.query(link, "INSERT INTO t1 VALUES (1, 'a')")
	if rows := p.rows(link, "SELECT v FROM t1"); len(rows) != 1 || rows[0][0] != "a" {
		t.Fatalf("lecture de t1 sous verrou = %q", rows)
	}
	for _, sql := range []string{"SELECT * FROM t2", "INSERT INTO t2 VALUES (2)", "SELECT * FROM t1 JOIN t2 ON t1.id = t2.id", "UPDATE t1 SET v = (SELECT COUNT(*) FROM t2)"} {
		if n := p.sqliteErrno(link, sql); n != 1100 {
			t.Errorf("%q sous LOCK TABLES t1 WRITE : errno %d ; attendu 1100", sql, n)
		} else if msg := p.str(p.call("mysqli_error", link)); msg != "Table 't2' was not locked with LOCK TABLES" {
			t.Errorf("%q : message %q", sql, msg)
		}
	}
	if v := types.Deref(p.z, p.call("mysqli_prepare", link, p.s("SELECT id FROM t2 WHERE id = ?"))); v.Type != types.TypeFalse {
		t.Error("mysqli_prepare sur t2 accepté sous LOCK TABLES t1 WRITE")
	} else if n := p.call("mysqli_errno", link).ToLong(); n != 1100 {
		t.Errorf("mysqli_prepare sur t2 : errno %d ; attendu 1100", n)
	}
	p.query(link, "UNLOCK TABLES")
	if rows := p.rows(link, "SELECT id FROM t2"); len(rows) != 1 {
		t.Fatalf("t2 après UNLOCK TABLES = %q", rows)
	}
	p.query(link, "LOCK TABLES t2 AS a READ, t1 LOW_PRIORITY WRITE")
	if n := p.sqliteErrno(link, "SELECT * FROM t2 AS a"); n != 0 {
		t.Errorf("t2 verrouillée sous l'alias a : errno %d", n)
	}
	if n := p.sqliteErrno(link, "INSERT INTO t2 VALUES (3)"); n != 1099 {
		t.Errorf("écriture dans t2 verrouillée en lecture : errno %d ; attendu 1099", n)
	}
	if n := p.sqliteErrno(link, "LOCK TABLES absente READ"); n != 1146 {
		t.Errorf("LOCK TABLES sur une table absente : errno %d ; attendu 1146", n)
	}
	p.query(link, "LOCK TABLES t2 AS a READ, t1 LOW_PRIORITY WRITE")
	p.query(link, "LOCK TABLES t1 READ")
	if n := p.sqliteErrno(link, "SELECT * FROM t2"); n != 1100 {
		t.Errorf("t2 après un second LOCK TABLES t1 READ : errno %d ; attendu 1100", n)
	}
	p.query(link, "UNLOCK TABLES")
	p.reset()

	path := filepath.Join(t.TempDir(), "locktx.sqlite")
	b, serr := openSQLiteBackend(path, "db")
	if serr != nil {
		t.Fatal(serr)
	}
	defer b.close()
	q := func(b *sqliteBackend, sql string) execResult {
		t.Helper()
		res, err := b.query(sql, false)
		if err != nil {
			t.Fatalf("%s : %v", sql, err)
		}
		return res[0]
	}
	q(b, "CREATE TABLE t1 (id INT NOT NULL, PRIMARY KEY (id))")
	q(b, "LOCK TABLES t1 WRITE")
	if !b.db.inTransaction() {
		t.Fatal("LOCK TABLES t1 WRITE : aucune transaction SQLite ouverte")
	}
	q(b, "INSERT INTO t1 VALUES (1)")
	q(b, "UNLOCK TABLES")
	if b.db.inTransaction() {
		t.Fatal("UNLOCK TABLES : transaction SQLite encore ouverte")
	}
	q(b, "LOCK TABLES t1 READ")
	if !b.db.inTransaction() {
		t.Fatal("LOCK TABLES t1 READ : aucune transaction SQLite ouverte")
	}
	q(b, "LOCK TABLES t1 WRITE")
	q(b, "INSERT INTO t1 VALUES (2)")
	// La fermeture relâche le verrou en validant, comme MySQL valide chaque instruction en autocommit.
	b.close()
	b2, serr := openSQLiteBackend(path, "db")
	if serr != nil {
		t.Fatal(serr)
	}
	defer b2.close()
	if r := q(b2, "SELECT id FROM t1 ORDER BY id"); r.rs == nil || len(r.rs.rows) != 2 {
		t.Fatalf("lignes écrites sous verrou après fermeture : %+v ; attendu 2", r.rs)
	}
}

// TestAlterTableRebuildKeepsForeignKeys vérifie que la reconstruction d'une table parente, en autocommit comme
// dans une transaction ouverte, ne supprime pas en cascade les lignes filles, que les clés étrangères restent
// contrôlées ensuite, que vues et déclencheurs sont recréés, et que MODIFY place ou retire AUTO_INCREMENT sans
// perdre la clé primaire.
func TestAlterTableRebuildKeepsForeignKeys(t *testing.T) {
	p := newPHP(t)
	path := filepath.Join(t.TempDir(), "rebuild.sqlite")
	link := p.connectSQLite(path)
	p.query(link, "CREATE TABLE parent (id INT NOT NULL, name VARCHAR(10), PRIMARY KEY (id))")
	p.query(link, "CREATE TABLE child (id INT NOT NULL, pid INT, PRIMARY KEY (id), FOREIGN KEY (pid) REFERENCES parent (id) ON DELETE CASCADE)")
	p.query(link, "CREATE TABLE audit (n INT)")
	p.query(link, "CREATE VIEW parent_names AS SELECT name FROM parent")
	// Le corps BEGIN ... END d'un déclencheur n'est pas traduit : il est créé par une seconde connexion SQLite.
	raw, serr := openSQLiteBackend(path, "db")
	if serr != nil {
		t.Fatal(serr)
	}
	if _, _, err := raw.run("CREATE TRIGGER parent_ins AFTER INSERT ON parent BEGIN INSERT INTO audit VALUES (NEW.id); END", nil); err != nil {
		t.Fatal(err)
	}
	raw.close()
	p.query(link, "INSERT INTO parent VALUES (1, 'a'), (2, 'b')")
	p.query(link, "INSERT INTO child VALUES (10, 1), (20, 2)")

	p.query(link, "ALTER TABLE parent MODIFY name VARCHAR(40) NOT NULL DEFAULT 'x'")
	p.query(link, "START TRANSACTION")
	p.query(link, "ALTER TABLE parent CHANGE name label VARCHAR(50)")
	p.query(link, "COMMIT")
	if rows := p.rows(link, "SELECT id, pid FROM child ORDER BY id"); !slices.EqualFunc(rows, [][]string{{"10", "1"}, {"20", "2"}}, slices.Equal) {
		t.Fatalf("lignes filles après reconstruction de la table parente = %q", rows)
	}
	if n := p.sqliteErrno(link, "INSERT INTO child VALUES (30, 99)"); n != 1452 {
		t.Fatalf("clé étrangère après reconstruction : errno %d ; attendu 1452", n)
	}
	if rows := p.rows(link, "SELECT * FROM parent_names ORDER BY 1"); !slices.EqualFunc(rows, [][]string{{"a"}, {"b"}}, slices.Equal) {
		t.Fatalf("vue après reconstruction = %q", rows)
	}
	p.query(link, "INSERT INTO parent (id) VALUES (3)")
	if rows := p.rows(link, "SELECT n FROM audit ORDER BY n"); !slices.EqualFunc(rows, [][]string{{"1"}, {"2"}, {"3"}}, slices.Equal) {
		t.Fatalf("déclencheur après reconstruction : audit = %q", rows)
	}

	p.query(link, "ALTER TABLE parent MODIFY id BIGINT NOT NULL AUTO_INCREMENT")
	p.query(link, "INSERT INTO parent (label) VALUES ('d')")
	if id := p.call("mysqli_insert_id", link).ToLong(); id != 4 {
		t.Fatalf("mysqli_insert_id après MODIFY AUTO_INCREMENT = %d ; attendu 4 (cache de métadonnées périmé)", id)
	}
	if rows := p.rows(link, "SELECT id FROM parent WHERE label = 'd'"); len(rows) != 1 || rows[0][0] != "4" {
		t.Fatalf("AUTO_INCREMENT posé par MODIFY : id = %q ; attendu 4", rows)
	}
	if desc := p.rows(link, "DESCRIBE parent"); desc[0][3] != "PRI" || desc[0][5] != "auto_increment" {
		t.Fatalf("DESCRIBE parent après MODIFY AUTO_INCREMENT = %q", desc)
	}
	p.query(link, "ALTER TABLE parent MODIFY id BIGINT NOT NULL")
	if desc := p.rows(link, "DESCRIBE parent"); desc[0][3] != "PRI" || desc[0][5] != "" {
		t.Fatalf("DESCRIBE parent après retrait d'AUTO_INCREMENT = %q", desc)
	}
	if n := p.sqliteErrno(link, "INSERT INTO parent VALUES (4, 'dup')"); n != 1062 {
		t.Fatalf("clé primaire après retrait d'AUTO_INCREMENT : errno %d ; attendu 1062", n)
	}
	if n := p.sqliteErrno(link, "ALTER TABLE parent MODIFY label VARCHAR(5) PRIMARY KEY"); n != 1068 {
		t.Fatalf("seconde clé primaire : errno %d ; attendu 1068", n)
	}
	if n := p.sqliteErrno(link, "ALTER TABLE parent MODIFY absente INT"); n != 1054 {
		t.Fatalf("MODIFY d'une colonne absente : errno %d ; attendu 1054", n)
	}
	if n := p.sqliteErrno(link, "ALTER TABLE absente MODIFY id INT"); n != 1146 {
		t.Fatalf("MODIFY sur une table absente : errno %d ; attendu 1146", n)
	}
	if n := p.sqliteErrno(link, "ALTER TABLE parent MODIFY"); n != 1064 {
		t.Fatalf("MODIFY sans définition : errno %d ; attendu 1064", n)
	}
	if tables := p.rows(link, "SHOW TABLES"); len(tables) != 4 {
		t.Fatalf("SHOW TABLES = %q ; aucune table __php127_alter_ ne doit subsister", tables)
	}
	p.reset()
}
