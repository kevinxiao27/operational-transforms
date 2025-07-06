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

type Operation struct {
	Type     OperationType
	Position int
	Text     string
	Length   int
	Version  int64
}

type OperationResult struct {
	Operation *Operation
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
	history    []*Operation
	mu         sync.RWMutex // read write mutex
}

func NewPlainTextDocument(documentID string) *PlainTextDocument {
	return &PlainTextDocument{
		documentID: documentID,
		content:    strings.Builder{},
		version:    0,
		history:    make([]*Operation, 0),
	}
}

// NewPlainTextDocumentWithContent creates a new document with initial content
func NewPlainTextDocumentWithContent(documentID, initialContent string) *PlainTextDocument {
	doc := &PlainTextDocument{
		documentID: documentID,
		content:    strings.Builder{},
		version:    0,
		history:    make([]*Operation, 0),
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

func (d *PlainTextDocument) GetHistory() []*Operation {
	d.mu.RLock()
	defer d.mu.RUnlock()

	history := make([]*Operation, len(d.history))
	copy(history, d.history)
	return history
}

func (o1 Operation) Equals(o2 Operation) bool {
	return o1.Type == o2.Type &&
		o1.Length == o2.Length &&
		o1.Position == o2.Position &&
		o1.Text == o2.Text
}

func (d *PlainTextDocument) isValidOperation(op *Operation) bool {
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

func (d *PlainTextDocument) ApplyOperation(operation *Operation) (*OperationResult, error) {
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
	versionedOp := &Operation{
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

func (d *PlainTextDocument) ApplyRemoteOperation(remoteOp *Operation, remoteVersion int64) (*OperationResult, error) {
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

func (d *PlainTextDocument) transformAgainstHistory(operation *Operation, baseVersion int64) (*Operation, error) {
	transformed := &Operation{
		Type:     operation.Type,
		Position: operation.Position,
		Text:     operation.Text,
		Length:   operation.Length,
		Version:  operation.Version,
	}

	for i := int(baseVersion); i < len(d.history); i++ {
		// historyOp := d.history[i]
		var err error
		// TODO implement transform
		// transformed, err = d.transform(transformed, historyOp, true)
		if err != nil {
			return nil, err
		}
	}

	return transformed, nil
}

func (d *PlainTextDocument) transform(op1, op2 *Operation) (*Operation, error) {
	return nil, &OTException{"TODO unimplemented", 500}
}

// func (d *PlainTextDocument) transform(op1, op2 *Operation, priority bool) (*Operation, error) {

// 	result := &Operation{
// 		Type:     op1.Type,
// 		Position: op1.Position,
// 		Text:     op1.Text,
// 		Length:   op1.Length,
// 		Version:  op1.Version,
// 	}

// 	// Basic transformation rules
// 	switch {
// 	case op1.Type == INSERT && op2.Type == INSERT:
// 		if op2.Position <= op1.Position {
// 			if op2.Position < op1.Position || priority {
// 				result.Position += len(op2.Text)
// 			}
// 		}
// 	case op1.Type == INSERT && op2.Type == DELETE:
// 		if op2.Position <= op1.Position {
// 			if op2.Position+op2.Length <= op1.Position {
// 				result.Position -= op2.Length
// 			} else {
// 				result.Position = op2.Position
// 			}
// 		}
// 	case op1.Type == DELETE && op2.Type == INSERT:
// 		if op2.Position <= op1.Position {
// 			result.Position += len(op2.Text)
// 		}
// 	case op1.Type == DELETE && op2.Type == DELETE:
// 		if op2.Position <= op1.Position {
// 			if op2.Position+op2.Length <= op1.Position {
// 				result.Position -= op2.Length
// 			} else {
// 				// Operations overlap, need more complex logic
// 				return nil, &OTException{
// 					Message: "Overlapping delete operations not supported in this simplified implementation",
// 				}
// 			}
// 		}
// 	}

// 	return result, nil
// }
