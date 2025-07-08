package model

import "errors"

type Op interface{}

type TextOperation struct {
	Ops          []Op
	BaseLength   int
	TargetLength int
}

func NewTextOperation() *TextOperation {
	return &TextOperation{}
}

func isRetain(op Op) bool {
	v, ok := op.(int)
	return ok && v > 0
}

func isInsert(op Op) bool {
	_, ok := op.(string)
	return ok
}

func isDelete(op Op) bool {
	v, ok := op.(int)
	return ok && v < 0
}

func (t *TextOperation) isNoop() bool {
	var ops = &t.Ops
	return len(*ops) == 0 || (len(*ops) == 1 && isRetain((*ops)[0]))
}

func (t *TextOperation) Retain(op Op) (*TextOperation, error) {
	var n int
	if isRetain(op) {
		n = op.(int)
	} else {
		return nil, errors.New("expected Retain operation")
	}

	if n == 0 {
		return t, nil
	}
	t.BaseLength += n
	t.TargetLength += n

	if len(t.Ops) == 0 {
		t.Ops = append(t.Ops, n)
		return t, nil
	}

	if last, ok := t.Ops[len(t.Ops)-1].(int); ok && isRetain(last) {
		t.Ops[len(t.Ops)-1] = last + n
	} else {
		t.Ops = append(t.Ops, n)
	}

	return t, nil
}

func (t *TextOperation) Insert(op Op) (*TextOperation, error) {
	var s string
	if isInsert(op) {
		s = op.(string)
	} else {
		return nil, errors.New("expected Insert operation")
	}

	if len(s) == 0 {
		return t, nil
	}

	var ops = &t.Ops
	var n = len(*ops)

	if len(*ops) == 0 {
		*ops = append(*ops, s)
		return t, nil
	}

	t.TargetLength += len(s)

	if last := (*ops)[n-1]; isInsert(last) {
		last = last.(string) + s // merging operations
	} else if isDelete(last) {
		// doesn't matter if del 3, "something", or vice versa
		// we enforce that the insert will come first
		// all operations will have same effect when applied to a document
		// of right length and equivalent using equals
		if len(*ops) <= 1 || !isInsert((*ops)[n-2]) {
			*ops = append(*ops, last)
			(*ops)[n-2] = s
		} else {
			(*ops)[n-2] = (*ops)[n-2].(string) + s // merge with insert before delete
		}
	} else {
		*ops = append(*ops, s)
	}

	return t, nil
}

func (t *TextOperation) Delete(op Op) (*TextOperation, error) {
	var n int

	// If op is a string, set n to its length
	if s, ok := op.(string); ok {
		n = -len(s)
	} else if i, ok := op.(int); ok {
		n = i
	} else {
		return nil, errors.New("delete expects an integer or a string")
	}

	if n == 0 {
		return t, nil
	}

	t.BaseLength -= n

	opsLen := len(t.Ops)
	if opsLen > 0 && isDelete(t.Ops[opsLen-1]) {
		// merge deletes, add lengths together
		t.Ops[opsLen-1] = t.Ops[opsLen-1].(int) + n
	} else {
		t.Ops = append(t.Ops, n)
	}

	return t, nil
}
