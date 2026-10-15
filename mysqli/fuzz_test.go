package mysqli

import (
	"bytes"
	"testing"
)

// FuzzHandshake sert les octets du fuzzer comme paquet de bienvenue d'un serveur local : la poignée de
// main rend une erreur ou une session, jamais une panique.
func FuzzHandshake(f *testing.F) {
	for _, n := range []int{0, 1, 5, 13, 14, 15, 32, 64} {
		f.Add(bytes.Repeat([]byte{0x41}, n))
		seed := append([]byte{10}, "5.7.0\x00"...)
		f.Add(append(seed, bytes.Repeat([]byte{0x41}, n)...))
	}
	f.Add(greetingPacket())
	f.Add([]byte{0xff, 0x15, 0x04, '#', '2', '8', '0', '0', '0', 'd', 'e', 'n', 'i', 'e', 'd'})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) >= maxPacket {
			t.Skip("paquet multiple hors du champ de cette cible")
		}
		_ = dialGreeting(t, data)
	})
}

// FuzzReadResult soumet des paquets arbitraires aux décodeurs du protocole, isolément puis enchaînés en
// résultat texte et binaire : chaque décodeur rend un refus ou une valeur, jamais une panique.
func FuzzReadResult(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x01, 0x02, 0x02, 0x00, 0x00, 0x00})
	f.Add([]byte{0xff, 0x48, 0x04, '#', 'H', 'Y', '0', '0', '0', 'x'})
	f.Add(colDefPacket("c", typeVarchar, 0))
	f.Add([]byte{0xfe, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})
	f.Add([]byte{0xfc, 0x01})
	f.Add(append([]byte{1}, append(colDefPacket("c", typeLong, 0), eofPacket...)...))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseOK(data)
		_ = parseErrPacket(data)
		_, _ = parseColumnDef(data)
		_, _, _, _ = readLenEncInt(data)
		_, _, _, _ = readLenEncString(data)
		_, _ = decodeTextRow(data, 3)
		_ = isEOF(data)
		_ = eofStatus(data)
		// Découpage déterministe en paquets : chaque octet de tête donne la longueur du paquet suivant.
		var packets [][]byte
		for p := data; len(p) > 0; {
			n := min(int(p[0])%64, len(p)-1)
			packets = append(packets, p[1:1+n])
			p = p[1+n:]
		}
		for _, binaryRows := range []bool{false, true} {
			b := fakeServer(t, packets...)
			_, _ = readResultSafe(t, b, binaryRows, "FuzzReadResult")
		}
	})
}

// FuzzLexAndTranslateSQL soumet des chaînes arbitraires à l'analyseur lexical, au découpage en
// instructions, à la liste blanche des verbes de tête et au traducteur que désigne le verbe de tête, sans
// jamais paniquer ; le
// texte de chaque jeton reproduit la source à l'octet près.
func FuzzLexAndTranslateSQL(f *testing.F) {
	for _, s := range []string{
		"", "SELECT 1", "SELECT * FROM `t` WHERE a = 'x\\'y' AND b = \"z\" -- c\n",
		wpOptionsDDL, wpPostmetaDDL,
		"INSERT INTO t (a, b) VALUES (1, 'x') ON DUPLICATE KEY UPDATE b = VALUES(b)",
		"UPDATE t SET a = a + 1 WHERE id IN (SELECT id FROM u) LIMIT 3",
		"DELETE a, b FROM a JOIN b ON a.id = b.id WHERE a.x = @v",
		"ALTER TABLE t ADD COLUMN c INT NOT NULL DEFAULT 0 AFTER b, DROP INDEX k",
		"DROP TABLE IF EXISTS a, b", "CREATE UNIQUE INDEX i ON t (a(10), b)",
		"/*!40101 SET NAMES utf8mb4 */; PRAGMA writable_schema = 1", "SELECT @@session.sql_mode, X'00ff', 0x1F, 1e+5",
		"SELECT (1", "SELECT CAST(a AS", "`unterminated", "'unterminated", "/* open",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, sql string) {
		toks := lexSQL(sql)
		var src bytes.Buffer
		for _, tk := range toks {
			src.WriteString(tk.text)
		}
		if !bytes.Contains([]byte(sql), []byte("/*!")) && src.String() != sql {
			t.Fatalf("jetons %q ne reproduisent pas la source %q", src.String(), sql)
		}
		_ = escapeString(sql)
		for _, st := range splitStatements(toks) {
			_ = countParams(st)
			if statementHeadError(st) != nil {
				continue
			}
			translateLikeExecStatement(st)
		}
	})
}

// translateLikeExecStatement appelle chaque traducteur sous la condition de tête qu'impose
// sqliteBackend.execStatement, seul appelant de production.
func translateLikeExecStatement(st []token) {
	w := firstWords(st, 3)
	tr := &translator{}
	switch w[0] {
	case "INSERT", "REPLACE":
		_, _, _ = tr.translateInsert(st)
	case "UPDATE":
		_ = tr.translateUpdate(st)
	case "DELETE":
		_, _, _ = tr.translateDelete(st)
	case "CREATE":
		switch {
		case w[1] == "TABLE" || w[1] == "TEMPORARY":
			_ = tr.translateCreateTable(st)
		case w[1] == "INDEX" || w[1] == "UNIQUE" || w[1] == "FULLTEXT" || w[1] == "SPATIAL":
			_ = tr.translateCreateIndex(st)
		default:
			_ = tr.expr(st)
		}
	case "ALTER":
		_, _ = tr.translateAlterTable(st)
	case "DROP":
		_ = tr.translateDrop(st)
	default:
		_ = tr.expr(st)
	}
}
