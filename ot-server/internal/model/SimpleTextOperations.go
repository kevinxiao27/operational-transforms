package model

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

type OperationType int

const (
	INSERT OperationType = iota
	DELETE
	RETAIN
)

type AtomicOperation struct {
	Type     OperationType
	Position int
	Text     string
	Length   int
	Version  int64
}

// type OperationType int

// const (
// 	INSERT OperationType = iota
// 	DELETE
// 	RETAIN
// )

var Noop = &AtomicOperation{Type: RETAIN, Position: 0, Text: "", Length: 0, Version: 0}

func CopyOperation(o *AtomicOperation) *AtomicOperation {
	return &AtomicOperation{o.Type, o.Position, o.Text, o.Length, o.Version}
}

type OperationResult struct {
	Operation *AtomicOperation
	Content   string
	Version   int64
}

type OTException struct {
	Message string
	Code    int
}

func (e *OTException) Error() string {
	return fmt.Sprintf("Error %d: %s", e.Code, e.Message)
}

type PlainTextDocument struct {
	content    strings.Builder
	version    int64
	documentID string
	history    []*AtomicOperation
	mu         sync.RWMutex // read write mutex
}

func NewPlainTextDocument(documentID string) *PlainTextDocument {
	return &PlainTextDocument{
		documentID: documentID,
		content:    strings.Builder{},
		version:    0,
		history:    make([]*AtomicOperation, 0),
	}
}

// NewPlainTextDocumentWithContent creates a new document with initial content
func NewPlainTextDocumentWithContent(documentID, initialContent string) *PlainTextDocument {
	doc := &PlainTextDocument{
		documentID: documentID,
		content:    strings.Builder{},
		version:    0,
		history:    make([]*AtomicOperation, 0),
	}
	doc.content.WriteString(initialContent)
	return doc
}

func (d *PlainTextDocument) GetContent() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.content.String()
}

func (d *PlainTextDocument) GetVersion() int64 {
	return atomic.LoadInt64(&d.version)
}

func (d *PlainTextDocument) GetDocumentID() string {
	return d.documentID
}

func (d *PlainTextDocument) GetHistory() []*AtomicOperation {
	d.mu.RLock()
	defer d.mu.RUnlock()

	history := make([]*AtomicOperation, len(d.history))
	copy(history, d.history)
	return history
}

func (o1 AtomicOperation) Equals(o2 AtomicOperation) bool {
	return o1.Type == o2.Type &&
		o1.Length == o2.Length &&
		o1.Position == o2.Position &&
		o1.Text == o2.Text
}

func (d *PlainTextDocument) isValidOperation(op *AtomicOperation) bool {
	contentLen := d.content.Len()

	switch op.Type {
	case INSERT:
		return op.Position >= 0 && op.Position <= contentLen && op.Text != ""
	case DELETE:
		return op.Position >= 0 && op.Position+op.Length <= contentLen && op.Length > 0
	case RETAIN:
		return op.Position >= 0 && op.Position+op.Length <= contentLen
	default:
		return false
	}
}

func (d *PlainTextDocument) ApplyOperation(operation *AtomicOperation) (*OperationResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.isValidOperation(operation) {
		return nil, &OTException{
			Message: fmt.Sprintf("Invalid operation: %+v", operation),
		}
	}

	switch operation.Type {
	case INSERT:
		content := d.content.String()
		newContent := content[:operation.Position] + operation.Text + content[operation.Position:]
		d.content.Reset()
		d.content.WriteString(newContent)

	case DELETE:
		content := d.content.String()
		endPos := operation.Position + operation.Length
		if endPos > len(content) {
			return nil, &OTException{
				Message: "Delete operation exceeds document length",
			}
		}
		newContent := content[:operation.Position] + content[endPos:]
		d.content.Reset()
		d.content.WriteString(newContent)

	case RETAIN:
		// Retain operations don't modify content, just advance position
		// Used for composing operations
		break
	}

	// Update version and history
	newVersion := atomic.AddInt64(&d.version, 1)
	versionedOp := &AtomicOperation{
		Type:     operation.Type,
		Position: operation.Position,
		Text:     operation.Text,
		Length:   operation.Length,
		Version:  newVersion,
	}
	d.history = append(d.history, versionedOp)

	return &OperationResult{
		Operation: versionedOp,
		Content:   d.content.String(),
		Version:   newVersion,
	}, nil
}

func (d *PlainTextDocument) ApplyRemoteOperation(remoteOp *AtomicOperation, remoteVersion int64) (*OperationResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// If versions match, apply directly
	if remoteVersion == atomic.LoadInt64(&d.version) {
		d.mu.Unlock() // Unlock before calling ApplyOperation which will lock again
		return d.ApplyOperation(remoteOp)
	}

	// Transform the remote operation against local operations
	transformedOp, err := d.transformAgainstHistory(remoteOp, remoteVersion)
	if err != nil {
		return nil, err
	}

	d.mu.Unlock() // Unlock before calling ApplyOperation which will lock again
	return d.ApplyOperation(transformedOp)
}

func (d *PlainTextDocument) transformAgainstHistory(operation *AtomicOperation, baseVersion int64) (*AtomicOperation, error) {
	transformed := &AtomicOperation{
		Type:     operation.Type,
		Position: operation.Position,
		Text:     operation.Text,
		Length:   operation.Length,
		Version:  operation.Version,
	}

	for i := int(baseVersion); i < len(d.history); i++ {
		historyOp := d.history[i]
		var err error
		// TODO implement transform
		transformed, _, err = d.transform(transformed, historyOp)
		if err != nil {
			return nil, err
		}
	}

	return transformed, nil
}

func (d *PlainTextDocument) transform(op1, op2 *AtomicOperation) (*AtomicOperation, *AtomicOperation, error) {

	switch {
	case op1.Type == RETAIN || op2.Type == RETAIN:
		return CopyOperation(op1),
			CopyOperation(op2),
			nil
	case op1.Type == INSERT && op2.Type == INSERT:
		if (op1.Position < op2.Position) || (op1.Position == op2.Position && op1.Text < op2.Text) {
			return CopyOperation(op1),
				&AtomicOperation{INSERT,
					op2.Position + op1.Length,
					op2.Text,
					op2.Length,
					op2.Version},
				nil
		}
		if (op2.Position < op1.Position) || (op1.Position == op2.Position && op1.Text > op2.Text) {
			return &AtomicOperation{INSERT,
					op1.Position + op2.Length,
					op1.Text,
					op1.Length,
					op1.Version},
				CopyOperation(op2),
				nil
		}

		// if operations are identical, local transformation has been acknowledged
		// results in no operation for both pending + remote queue, and it can be safely discarded
		return Noop, Noop, nil

	case op1.Type == INSERT && op2.Type == DELETE:
		if op1.Position <= op2.Position {
			return CopyOperation(op1),
				&AtomicOperation{DELETE, op2.Position + op1.Length, op2.Text, op2.Length, op2.Version},
				nil
		}
		if op1.Position >= op2.Position+op2.Length {
			return &AtomicOperation{INSERT, op1.Position - op2.Length, op1.Text, op1.Length, op1.Version}, CopyOperation(op2), nil
		}

		// Insert operation of op1 must be deleted
		// doesn't prove intention but maintains transformational property
		return Noop,
			&AtomicOperation{DELETE, op1.Position, "", op1.Length + op2.Length, op1.Version},
			nil

	case op1.Type == DELETE && op2.Type == INSERT:
		if op1.Position >= op2.Position {
			return &AtomicOperation{DELETE, op1.Position + op2.Length, "", op1.Length, op1.Version}, CopyOperation(op2), nil
		}

		if op1.Position+op1.Length <= op2.Position {
			return CopyOperation(op1),
				&AtomicOperation{INSERT, op2.Position - op1.Length, op2.Text, op2.Length, op2.Version},
				nil
		}

		// same issue as above
		return &AtomicOperation{DELETE, op1.Position, "", op1.Length + op2.Length, op1.Version},
			Noop,
			nil

	case op1.Type == DELETE && op2.Type == DELETE:
		if op1.Position == op2.Position {
			if op1.Length == op2.Length {
				return Noop, Noop, nil
			} else if op1.Length < op2.Length {
				return Noop,
					&AtomicOperation{DELETE,
						op2.Position,
						"",
						op2.Length - op1.Length, // remaining unapplied diff
						op2.Version},
					nil
			}
			return &AtomicOperation{DELETE, op1.Position, "", op1.Length - op2.Length, op1.Version},
				Noop,
				nil
		}
		if op1.Position < op2.Position {
			if op1.Position+op1.Length <= op2.Position {
				// op1 fully before op2
				return CopyOperation(op1),
					&AtomicOperation{DELETE, op2.Position - op1.Length, "", op2.Length, op2.Version}, nil
			}
			if op1.Position+op1.Length <= op2.Position+op2.Length { // op1 fully covers op2
				// op1 transformed to only include existing diff, op2 applied (noop)
				return &AtomicOperation{DELETE, op2.Position, "", op1.Length - op2.Length, op1.Version}, Noop, nil
			}
			// Partial overlap
			// op1 -> overlap -> op2
			// op1 takes size from op1 to op2
			// op2 takes diff from op2 to end
			return &AtomicOperation{DELETE, op1.Position, "", op2.Position - op1.Position, op1.Version}, //
				&AtomicOperation{DELETE, op1.Position, "", op2.Position + op2.Length - (op1.Position + op1.Length), op2.Version},
				nil
		}
		if op1.Position > op2.Position {
			if op1.Position >= op2.Position+op2.Length {
				// op1 is fully after op2
				return &AtomicOperation{DELETE, op1.Position - op2.Length, "", op1.Length, op1.Version},
					CopyOperation(op2),
					nil
			}
			if op1.Position+op1.Length <= op2.Position+op2.Length {
				// op1 is fully within op2 — noop
				return Noop,
					&AtomicOperation{DELETE, op2.Position, "", op2.Length - op1.Length, op2.Version},
					nil
			}
			// Partial overlap
			// op2 -> overlap -> op1
			// op1 takes size from op1 to end
			// op2 takes diff from op2 to op1
			return &AtomicOperation{DELETE, op2.Position, "", op1.Position + op1.Length - (op2.Position + op2.Length), op1.Version},
				&AtomicOperation{DELETE, op2.Position, "", op1.Position - op2.Position, op2.Version},
				nil
		}

	}
	return nil, nil, &OTException{fmt.Sprintf("Unknown transform cases %d, %d", op1.Type, op2.Type), 500}
}
