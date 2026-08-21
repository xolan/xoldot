package managedhome

import (
	"fmt"
	"testing"
)

func TestTransactionRollsBackInReverseMutationOrder(t *testing.T) {
	var order []string
	transaction := linkTransaction{}
	for _, name := range []string{"first", "second", "third"} {
		transaction.append(func() error {
			order = append(order, name)
			return nil
		}, nil)
	}
	if err := transaction.rollback(); err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(order), "[third second first]"; got != want {
		t.Errorf("rollback order = %s, want %s", got, want)
	}
}
