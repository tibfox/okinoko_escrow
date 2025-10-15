package main

import (
	"encoding/json"
	"fmt"
	"okinoko_escrow/sdk"
	"strconv"
)

func main() {
	// exact code within other files of conrtact folder
}

const (
	maxNameLength = 100 // maxNameLength for an escrow
)

type EscrowAccount struct {
	Address      sdk.Address `json:"address"`
	Agree        *bool       `json:"agree"`
	DecisionTxID *string     `json:"decisionTx"`
}

type Escrow struct {
	ID           uint64        `json:"id"`
	Name         string        `json:"name"`
	From         EscrowAccount `json:"from"`
	To           EscrowAccount `json:"to"`
	Arbitrator   EscrowAccount `json:"arb"`
	CreationTxID string        `json:"creationTx"`
	Amount       float64       `json:"amount"`
	Asset        sdk.Asset     `json:"asset"`
	Closed       bool          `json:"closed"` // false = open / true = closed
	Outcome      string        `json:"outcome"`
}

type CreateEscrowArgs struct {
	Name       string `json:"name"`
	To         string `json:"to"`
	Arbitrator string `json:"arb"`
}

type DecisionArgs struct {
	EscrowID *uint64 `json:"id"`
	Decision *bool   `json:"d"`
}

const (
	OutcomeRelease = "release"
	OutcomeRefund  = "refund"
	OutcomePending = "pending"
)

//go:wasmexport e_create
func CreateEscrow(payload *string) *string {
	input := FromJSON[CreateEscrowArgs](*payload, "escrow args")
	creator := sdk.GetEnvKey("msg.sender")
	txID := sdk.GetEnvKey("tx.id")
	input.Validate(*creator)

	escrowID := newEscrowID()
	ta := GetFirstTransferAllow(sdk.GetEnv().Intents)
	if ta == nil {
		sdk.Abort("intent needed")
	}
	if ta.Limit <= 0 {
		sdk.Abort("intent >0 needed")
	}
	if !isValidAsset(ta.Token.String()) {
		sdk.Abort("intent asset not supported")
	}
	if input.To == *creator {
		sdk.Abort("receiver must differ from sender")
	}
	escrow := Escrow{
		ID: escrowID,
		From: EscrowAccount{
			Address: sdk.Address(*creator),
			Agree:   nil,
		},
		To: EscrowAccount{
			Address: sdk.Address(input.To),
			Agree:   nil,
		},
		Arbitrator: EscrowAccount{
			Address: sdk.Address(input.Arbitrator),
			Agree:   nil,
		},
		Name:         input.Name,
		CreationTxID: *txID,
		Amount:       ta.Limit,
		Asset:        ta.Token,
		Outcome:      OutcomePending,
	}
	sdk.HiveDraw(int64(ta.Limit*1000), ta.Token)
	saveEscrow(&escrow)
	setCount(EscrowCount, escrow.ID+1)
	// Emit creation event.
	EmitEscrowCreatedEvent(
		escrow.ID,
		escrow.From.Address.String(),
		escrow.To.Address.String(),
		escrow.Arbitrator.Address.String(),
		escrow.Amount,
		escrow.Asset.String(), *txID)

	result := UInt64ToString(escrowID)
	return &result
}

//go:wasmexport e_decide
func AddDecision(payload *string) *string {
	input := FromJSON[DecisionArgs](*payload, "Decision args")
	input.Validate()
	print(input)
	e := loadEscrow(*input.EscrowID)
	if e.Closed {
		sdk.Abort("escrow closed")
	}

	sender := sdk.GetEnvKey("msg.sender")

	role := getRoleOfSender(e, sender)
	if role == "" {
		sdk.Abort("sender not part of the escrow")
	}

	txID := sdk.GetEnvKey("tx.id")
	escrowAccount := EscrowAccount{
		Address:      sdk.Address(*sender),
		Agree:        input.Decision,
		DecisionTxID: txID,
	}
	if *sender == e.From.Address.String() {
		e.From = escrowAccount
	} else if *sender == e.To.Address.String() {
		e.To = escrowAccount
	} else if *sender == e.Arbitrator.Address.String() {
		e.Arbitrator = escrowAccount
	}
	EmitEscrowDecisionEvent(e.ID, role, *sender, *input.Decision, *txID)
	e.EvaluateEscrowOutcome()
	if e.Closed {
		EmitEscrowClosedEvent(e.ID, e.Outcome, *txID)
	}
	saveEscrow(e)
	return nil
}

// GETTERS

//go:wasmexport e_get
func GetEscrow(id *string) *string {
	escrow := loadEscrow(StringToUInt64(id))
	jsonStr := ToJSON(escrow, "escrow")
	return &jsonStr
}

// STATE PERSISTENCE & LOADING

func saveEscrow(escrow *Escrow) error {
	b, err := json.Marshal(escrow)
	if err != nil {
		sdk.Abort("failed to marshal escrow")
	}

	// Save escrow object.
	idKey := escrowKey(escrow.ID)
	sdk.StateSetObject(idKey, string(b))
	return nil
}

func loadEscrow(id uint64) *Escrow {
	key := escrowKey(id)
	ptr := sdk.StateGetObject(key)
	if ptr == nil || *ptr == "" {
		sdk.Abort(fmt.Sprintf("escrow %d not found", id))
	}
	collection := FromJSON[Escrow](*ptr, "escrow")
	return collection
}

// VALIDATORS

func (c *CreateEscrowArgs) Validate(callerAddress string) {
	if c.Name == "" {
		sdk.Abort("name is mandatory")
	}
	if len(c.Name) > maxNameLength {
		sdk.Abort("name too long")
	}
	if c.To == "" {
		sdk.Abort("receiver is mandatory")
	}
	if c.Arbitrator == "" {
		sdk.Abort("arbitrator is mandatory")
	}
	if c.Arbitrator == c.To || c.Arbitrator == callerAddress {
		sdk.Abort("arbitrator must be 3rd party")
	}
}

func (c *DecisionArgs) Validate() {
	if c.EscrowID == nil {
		sdk.Abort("escrow id is mandatory")
	}
	if c.Decision == nil {
		sdk.Abort("decision is not a correct boolean")
	}
}

// COMMON HELPERS
func escrowKey(escrowID uint64) string {
	return "e:" + strconv.FormatUint(escrowID, 10)
}

func newEscrowID() uint64 {
	return getCount(EscrowCount)
}

func getRoleOfSender(e *Escrow, sender *string) string {
	switch *sender {
	case e.From.Address.String():
		return "From"
	case e.To.Address.String():
		return "To"
	case e.Arbitrator.Address.String():
		return "Arbitrator"
	default:
		return ""
	}
}

func (e *Escrow) CountDecisions() (releaseCount, refundCount int) {
	accounts := []EscrowAccount{e.From, e.To, e.Arbitrator}

	for _, acc := range accounts {
		if acc.Agree == nil {
			continue // no decision yet
		}
		if *acc.Agree {
			releaseCount++
		} else {
			refundCount++
		}
	}
	return
}

func (e *Escrow) EvaluateEscrowOutcome() {
	releaseCount, refundCount := e.CountDecisions()

	if releaseCount >= 2 {
		e.Closed = true
		e.Outcome = OutcomeRelease
		sdk.HiveTransfer(e.To.Address, int64(e.Amount*1000), e.Asset)
	} else if refundCount >= 2 {
		e.Closed = true
		e.Outcome = OutcomeRefund
		sdk.HiveTransfer(e.From.Address, int64(e.Amount*1000), e.Asset)
	}
}

// GENERAL HELPERS

// Conversions from/to json strings

func ToJSON[T any](v T, objectType string) string {
	b, err := json.Marshal(v)
	if err != nil {
		sdk.Abort("failed to marshal " + objectType)
	}
	return string(b)
}

func FromJSON[T any](data string, objectType string) *T {
	// data = strings.TrimSpace(data)
	var v T
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		sdk.Abort(
			fmt.Sprintf("failed to unmarshal %s \ninput: %s\nerror: %v", objectType, data, err.Error()))
	}
	return &v
}

func StringToUInt64(ptr *string) uint64 {
	if ptr == nil {
		sdk.Abort("input is empty")
	}
	val, err := strconv.ParseUint(*ptr, 10, 64) // base 10, 64-bit
	if err != nil {
		sdk.Abort(fmt.Sprintf("failed to parse '%s' to uint64: %v", *ptr, err))
	}
	return val
}

func UInt64ToString(val uint64) string {
	return strconv.FormatUint(val, 10)
}

// indexing helpers

const (
	NFTsCount   = "cnt:n" //                  // copy & paste error (have to keep it in the code so the contract is verifyable)
	EscrowCount = "cnt:e" //                  // holds a int counter for escrows (to create new ids)
)

// ---- helpers ----

func getCount(key string) uint64 {
	ptr := sdk.StateGetObject(key)
	if ptr == nil || *ptr == "" {
		return 0
	}
	return StringToUInt64(ptr)
}

func setCount(key string, n uint64) {
	sdk.StateSetObject(key, UInt64ToString(n))
}

// ---------- Transfer Intent Helpers ----------

// TransferAllow represents a parsed transfer.allow intent,
// including the limit (amount) and token (asset).
type TransferAllow struct {
	Limit float64
	Token sdk.Asset
}

// validAssets defines the list of supported assets for transfer intents.
var validAssets = []string{sdk.AssetHbd.String(), sdk.AssetHive.String()}

// isValidAsset checks whether a given token string is a supported asset.
func isValidAsset(token string) bool {
	for _, a := range validAssets {
		if token == a {
			return true
		}
	}
	return false
}

// GetFirstTransferAllow searches the provided intents and returns the first
// valid transfer.allow intent as a TransferAllow. Returns nil if none exist.
func GetFirstTransferAllow(intents []sdk.Intent) *TransferAllow {
	for _, intent := range intents {
		if intent.Type == "transfer.allow" {
			token := intent.Args["token"]
			// If we have a transfer.allow intent but the asset is not valid, abort.
			if !isValidAsset(token) {
				sdk.Abort("invalid intent token")
			}
			limitStr := intent.Args["limit"]
			limit, err := strconv.ParseFloat(limitStr, 64)
			if err != nil {
				sdk.Abort("invalid intent limit")
			}
			ta := &TransferAllow{
				Limit: limit,
				Token: sdk.Asset(token),
			}
			return ta
		}
	}
	return nil
}

// EVENTS

// Event represents a generic event emitted by the contract.
type Event struct {
	Type       string            `json:"type"`       // Type is the kind of event (e.g., "mint", "transfer").
	Attributes map[string]string `json:"attributes"` // Attributes are key/value pairs with event data.
	TxID       string            `json:"tx"`
}

// emitEvent constructs and logs an event as JSON.
func emitEvent(eventType string, attributes map[string]string, txID string) {
	event := Event{
		Type:       eventType,
		Attributes: attributes,
		TxID:       txID,
	}
	sdk.Log(ToJSON(event, eventType+" event data"))
}

// EmitEscrowCreatedEvent emits an event for creating a new escrow.
func EmitEscrowCreatedEvent(escrowID uint64, fromAddress string, toAddress string, arbAddress string, amount float64, asset string, txID string) {
	emitEvent("escrow_created", map[string]string{
		"id":     UInt64ToString(escrowID),
		"from":   fromAddress,
		"to":     toAddress,
		"arb":    arbAddress,
		"amount": strconv.FormatFloat(amount, 'f', -1, 64),
		"asset":  asset,
	}, txID)
}

// EmitEscrowDecisionEvent emits an event for a new decision.
func EmitEscrowDecisionEvent(escrowID uint64, role string, address string, decision bool, txID string) {
	emitEvent("escrow_decision", map[string]string{
		"id":        UInt64ToString(escrowID),
		"byRole":    role,
		"byAddress": address,
		"decision":  strconv.FormatBool(decision),
	}, txID)
}

// EmitEscrowClosedEvent emits an event for closing an escrow.
func EmitEscrowClosedEvent(escrowID uint64, outcome string, txID string) {
	emitEvent("escrow_closed", map[string]string{
		"id":      UInt64ToString(escrowID),
		"outcome": outcome,
	}, txID)
}
