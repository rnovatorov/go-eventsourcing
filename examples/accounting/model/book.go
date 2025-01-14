package model

import (
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rnovatorov/go-eventsource/examples/accounting/accountingpb"
	"github.com/rnovatorov/go-eventsource/pkg/eventsource"
)

type Book struct {
	created      bool
	closed       bool
	description  string
	transactions []Transaction
	accounts     map[string]*Account
}

func (b *Book) Closed() bool {
	return b.closed
}

func (b *Book) Description() string {
	return b.description
}

func (b *Book) AccountByName(name string) (*Account, error) {
	if account, ok := b.accounts[name]; ok {
		return account, nil
	}

	return nil, ErrAccountNotFound
}

func (b *Book) ProcessCommand(
	command eventsource.Command,
) (eventsource.StateChanges, error) {
	switch cmd := command.(type) {
	case BookCreate:
		return b.processCreate(cmd)
	case BookClose:
		return b.processClose(cmd)
	case BookAccountAdd:
		return b.processAccountAdd(cmd)
	case BookTransactionEnter:
		return b.processTransactionEnter(cmd)
	default:
		return nil, fmt.Errorf("%w: %T", eventsource.ErrCommandUnknown, cmd)
	}
}

func (b *Book) processCreate(cmd BookCreate) (eventsource.StateChanges, error) {
	if b.created {
		return nil, ErrBookAlreadyCreated
	}

	if b.closed {
		return nil, ErrBookClosed
	}

	return eventsource.StateChanges{
		&accountingpb.BookCreated{
			Description: cmd.Description,
		},
	}, nil
}

func (b *Book) processClose(BookClose) (eventsource.StateChanges, error) {
	if b.closed {
		return nil, ErrBookClosed
	}

	return eventsource.StateChanges{
		&accountingpb.BookClosed{},
	}, nil
}

func (b *Book) processAccountAdd(cmd BookAccountAdd) (eventsource.StateChanges, error) {
	if b.closed {
		return nil, ErrBookClosed
	}

	if _, ok := b.accounts[cmd.AccountName]; ok {
		return nil, ErrAccountNameConflict
	}

	if cmd.AccountName == "" {
		return nil, ErrAccountNameEmpty
	}

	if cmd.AccountType == accountingpb.AccountType_UNKNOWN {
		return nil, ErrAccountTypeUnknown
	}

	return eventsource.StateChanges{
		&accountingpb.BookAccountAdded{
			Name: cmd.AccountName,
			Type: cmd.AccountType,
		},
	}, nil
}

func (b *Book) processTransactionEnter(
	cmd BookTransactionEnter,
) (eventsource.StateChanges, error) {
	if b.closed {
		return nil, ErrBookClosed
	}

	accountDebited, ok := b.accounts[cmd.Transaction.AccountDebited]
	if !ok {
		return nil, ErrAccountDebitedNotFound
	}

	accountCredited, ok := b.accounts[cmd.Transaction.AccountCredited]
	if !ok {
		return nil, ErrAccountCreditedNotFound
	}

	accountDebitedNewBalance, err := accountDebited.canDebit(cmd.Transaction.Amount)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAccountDebitDeclined, err)
	}

	accountCreditedNewBalance, err := accountCredited.canCredit(cmd.Transaction.Amount)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAccountCreditDeclined, err)
	}

	return eventsource.StateChanges{
		&accountingpb.BookTransactionEntered{
			Timestamp:                 timestamppb.New(cmd.Transaction.Timestamp),
			AccountDebited:            cmd.Transaction.AccountDebited,
			AccountCredited:           cmd.Transaction.AccountCredited,
			Amount:                    cmd.Transaction.Amount,
			AccountDebitedNewBalance:  accountDebitedNewBalance,
			AccountCreditedNewBalance: accountCreditedNewBalance,
		},
	}, nil
}

func (b *Book) ApplyStateChange(stateChange eventsource.StateChange) {
	switch sc := stateChange.(type) {
	case *accountingpb.BookCreated:
		b.applyCreated(sc)
	case *accountingpb.BookClosed:
		b.applyClosed(sc)
	case *accountingpb.BookAccountAdded:
		b.applyAccountAdded(sc)
	case *accountingpb.BookTransactionEntered:
		b.applyTransactionEntered(sc)
	default:
		panic(fmt.Sprintf("unexpected state change: %T", sc))
	}
}

func (b *Book) applyCreated(sc *accountingpb.BookCreated) {
	b.created = true
	b.description = sc.Description
	b.accounts = make(map[string]*Account)
}

func (b *Book) applyClosed(*accountingpb.BookClosed) {
	b.closed = true
}

func (b *Book) applyAccountAdded(sc *accountingpb.BookAccountAdded) {
	b.accounts[sc.Name] = &Account{
		name:    sc.Name,
		type_:   sc.Type,
		balance: 0,
	}
}

func (b *Book) applyTransactionEntered(sc *accountingpb.BookTransactionEntered) {
	b.accounts[sc.AccountDebited].balance = sc.AccountDebitedNewBalance
	b.accounts[sc.AccountCredited].balance = sc.AccountCreditedNewBalance

	b.transactions = append(b.transactions, Transaction{
		Timestamp:       sc.Timestamp.AsTime(),
		AccountDebited:  sc.AccountDebited,
		AccountCredited: sc.AccountCredited,
		Amount:          sc.Amount,
	})
}
