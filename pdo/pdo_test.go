// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/pdo: Tests unitaires du moteur relationnel in-process et transactions

package pdo

import (
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
)

func TestPDOMemoryExecution(t *testing.T) {
	db := NewMemoryDB()

	// 1. Create table
	_, err := db.Exec("CREATE TABLE users (name, email)")
	if err != nil {
		t.Fatalf("CreateTable error: %v", err)
	}

	// 2. Insert with explicit columns
	_, err = db.Exec("INSERT INTO users (name, email) VALUES (?, ?)", types.NewString("Alice"), types.NewString("alice@example.com"))
	if err != nil {
		t.Fatalf("Insert error: %v", err)
	}

	if db.LastInsertId() != 1 {
		t.Fatalf("Attendu lastInsertId = 1, got: %d", db.LastInsertId())
	}

	_, err = db.Exec("INSERT INTO users (name, email) VALUES (?, ?)", types.NewString("Bob"), types.NewString("bob@example.com"))
	if err != nil {
		t.Fatalf("Insert error 2: %v", err)
	}

	// 3. Query with WHERE clause
	stmt := &Statement{query: "SELECT name, email FROM users WHERE id = ?", db: db}
	ok, err := stmt.Execute([]types.Value{types.NewLong(1)})
	if !ok || err != nil {
		t.Fatalf("Select execute error: %v", err)
	}

	if stmt.RowCount() != 1 {
		t.Fatalf("Attendu 1 ligne pour id=1, got: %d", stmt.RowCount())
	}

	row1, hasNext := stmt.Fetch()
	if !hasNext || row1["name"].String() != "Alice" {
		t.Fatalf("Row1 inattendue: %v", row1)
	}

	// 4. Update
	_, err = db.Exec("UPDATE users SET name = ? WHERE id = ?", types.NewString("Alice Cooper"), types.NewLong(1))
	if err != nil {
		t.Fatalf("Update error: %v", err)
	}

	// 5. Delete with WHERE
	n, err := db.Exec("DELETE FROM users WHERE id = ?", types.NewLong(2))
	if err != nil || n != 1 {
		t.Fatalf("Delete error: %v, deleted: %d", err, n)
	}
}

func TestPDOTransactionsRollback(t *testing.T) {
	db := NewMemoryDB()
	_, _ = db.Exec("CREATE TABLE accounts (balance)")
	_, _ = db.Exec("INSERT INTO accounts (balance) VALUES (?)", types.NewLong(1000))

	// Begin transaction
	if err := db.Begin(); err != nil {
		t.Fatalf("Begin error: %v", err)
	}

	// Insert row inside transaction
	_, _ = db.Exec("INSERT INTO accounts (balance) VALUES (?)", types.NewLong(500))

	// Rollback transaction
	if err := db.Rollback(); err != nil {
		t.Fatalf("Rollback error: %v", err)
	}

	// Query accounts: should only have initial row
	rows, _ := db.Query("SELECT * FROM accounts")
	if len(rows) != 1 {
		t.Fatalf("Attendu 1 ligne après rollback, got: %d", len(rows))
	}
}
