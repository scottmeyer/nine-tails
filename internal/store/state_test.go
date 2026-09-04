package store

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestPutStateMetadataRollback(t *testing.T) {
	s := openTest(t)
	nr := NewRecord{Agent: "a", Lane: "state", Kind: "working-state", Name: "working", Body: "status: old", Meta: Meta{"repo-id": {"a"}}}
	var first *Record
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		first, err = PutState(tx, nr, "none", true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("abort after insertion")
	for _, preserve := range []bool{true, false} {
		err := s.Tx(func(tx *sql.Tx) error {
			nr.Body, nr.Meta = "status: new", nil
			if _, err := PutState(tx, nr, first.ID, preserve); err != nil {
				return err
			}
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatal(err)
		}
		current, err := ActiveNamed(s.DB, "a", "state", "working-state", "working")
		if err != nil || current.ID != first.ID || current.Body != first.Body || !reflect.DeepEqual(current.Meta, first.Meta) {
			t.Fatalf("rollback lost original state: %+v, %v", current, err)
		}
		history, err := ListRecords(s.DB, Filter{Agent: "a", Lane: "state", Status: "*"})
		if err != nil || len(history) != 1 {
			t.Fatalf("rollback retained an extra version: %+v, %v", history, err)
		}
	}
}

func TestPutStateRejectsOtherLanes(t *testing.T) {
	s := openTest(t)
	err := s.Tx(func(tx *sql.Tx) error {
		_, err := PutState(tx, NewRecord{Agent: "a", Lane: "definition", Kind: "custom", Name: "x", Body: "body"}, "none", true)
		return err
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong tuple: %v", err)
	}
}
