package main

import (
	"encoding/json"
	"fmt"
	"okinoko_escrow/sdk"
	"strconv"
	"strings"
)

func main() {
	// exact code within other files of conrtact folder
}

const (
	maxNameLength = 100 // maxNameLength for an escrow

	DecisionUnset   uint8 = 0
	DecisionRefund  uint8 = 1
	DecisionRelease uint8 = 2
)

type EscrowAccount struct {
	Address  string `json:"address"`
	Decision uint8  `json:"decision"`
}

type Escrow struct {
	ID         uint64        `json:"id"`
	Name       string        `json:"name"`
	From       EscrowAccount `json:"from"`
	To         EscrowAccount `json:"to"`
	Arbitrator EscrowAccount `json:"arb"`
	Amount     float64       `json:"amount"`
	Asset      string        `json:"asset"`
	Closed     bool          `json:"closed"` // false = open / true = closed
	Outcome    uint8         `json:"outcome"`
}

type CreateEscrowArgs struct {
	Name       string // n
	To         string // t
	Arbitrator string // a
}

type DecisionArgs struct {
	EscrowID uint64 // id
	Decision uint8  // d
}

func CsvToCreateEscrowArgs(csv *string) CreateEscrowArgs {
	if csv == nil || *csv == "" {
		sdk.Abort("input CSV is nil or empty")
	}

	parts := strings.Split(*csv, "|")
	if len(parts) != 3 {
		sdk.Abort("invalid CSV format: expected 3 fields (Name|To|Arbitrator)")
	}

	return CreateEscrowArgs{
		Name:       parts[0],
		To:         parts[1],
		Arbitrator: parts[2],
	}
}

func CsvToDecisionArgs(csv *string) DecisionArgs {
	if csv == nil || *csv == "" {
		sdk.Abort("input CSV is nil or empty")
	}

	data := *csv
	sep := strings.IndexByte(data, '|')
	if sep == -1 {
		sdk.Abort("invalid CSV format: expected EscrowID|Decision")
	}

	// Parse EscrowID (before '|')
	escrowIDValue, err := strconv.ParseUint(data[:sep], 10, 64)
	if err != nil {
		sdk.Abort("invalid EscrowID: must be a number")
	}

	// Parse decision (after '|')
	decStr := data[sep+1:] // substring decision
	// trim spaces manually (no alloc)
	i, j := 0, len(decStr)-1
	for i <= j && decStr[i] == ' ' {
		i++
	}
	for j >= i && decStr[j] == ' ' {
		j--
	}
	decStr = decStr[i : j+1]
	decLen := len(decStr)

	var decision uint8

	// Minimal, strict check (case-insensitive)
	if decLen == 4 && (decStr == "true" || decStr == "TRUE" || decStr == "True") {
		decision = DecisionRelease
	} else if decLen == 5 && (decStr == "false" || decStr == "FALSE" || decStr == "False") {
		decision = DecisionRefund
	} else if decLen == 1 && decStr[0] == '1' {
		decision = DecisionRelease
	} else if decLen == 1 && decStr[0] == '0' {
		decision = DecisionRefund
	} else {
		sdk.Abort("invalid decision: must be true/false or 1/0")
	}

	return DecisionArgs{
		EscrowID: escrowIDValue,
		Decision: decision,
	}
}

func CsvToReward(csv *string) (uint64, string) {
	if csv == nil || *csv == "" {
		sdk.Abort("reward is empty")
	}

	data := *csv
	sep := strings.IndexByte(data, '|')
	if sep == -1 {
		sdk.Abort("invalid reward format, expected amount|asset")
	}

	amount, err := strconv.ParseUint(data[:sep], 10, 64)
	if err != nil {
		sdk.Abort("error parsing amount")
	}
	return amount, data[sep+1:]
}

//go:wasmexport e_create
func CreateEscrow(payload *string) *string {
	input := CsvToCreateEscrowArgs(payload)
	creator := sdk.GetEnvKey("msg.sender")

	input.Validate(*creator)

	escrowID := newEscrowID()
	ta := GetFirstTransferAllow(sdk.GetEnv().Intents)
	if ta == nil {
		sdk.Abort("intent needed")
	}
	if ta.LimitMilli <= 0 {
		sdk.Abort("intent >0 needed")
	}
	if !isValidAsset(ta.Token.String()) {
		sdk.Abort("intent asset not supported")
	}
	if input.To == *creator {
		sdk.Abort("receiver must differ from sender")
	}

	sdk.HiveDraw(int64(ta.LimitMilli), ta.Token)
	saveEscrowBase(escrowID, input.Name)
	var sb strings.Builder
	sb.Grow(len(*creator) + len(input.To) + len(input.Arbitrator) + 2)
	sb.WriteString(*creator)
	sb.WriteByte('|')
	sb.WriteString(input.To)
	sb.WriteByte('|')
	sb.WriteString(input.Arbitrator)
	saveEscrowParties(escrowID, sb.String())

	saveEscrowReward(escrowID, ta.LimitMilli, ta.Token.String())

	saveEscrowDecisions(escrowID, []uint8{DecisionUnset, DecisionUnset, DecisionUnset})

	txID := sdk.GetEnvKey("tx.id")
	EmitEscrowCreatedEvent(
		escrowID,
		*creator,
		input.To,
		input.Arbitrator,
		float64(ta.LimitMilli)/1000,
		ta.Token.String(), *txID)

	result := strconv.FormatUint(escrowID, 10)
	return &result
}

//go:wasmexport e_decide
func AddDecision(payload *string) *string {
	input := CsvToDecisionArgs(payload)
	roles := loadRoles(input.EscrowID)
	sender := sdk.GetEnvKey("msg.sender")

	role := getRoleOfSender(sender, roles)
	if role == nil {
		sdk.Abort("sender not part of the escrow")
	}

	// Step 2: Load all decisions
	decs := loadDecisions(input.EscrowID)

	// Step 3: Check if closed
	c, _ := getEscrowOutcome(decs)
	if c {
		sdk.Abort("escrow already closed")
	}

	// Step 4: Set decision for this role
	roleIndex := *role // dereference the pointer to get the uint8 value
	// if int(roleIndex) >= len(decs) {
	// 	sdk.Abort("invalid role index for decision array") // will probably never happen..
	// }
	decs[roleIndex] = input.Decision

	// Step 5: Save decisions back to state
	saveEscrowDecisions(input.EscrowID, decs)
	txID := sdk.GetEnvKey("tx.id")
	processEscrowOutcome(input.EscrowID, decs, *txID)
	EmitEscrowDecisionEvent(
		input.EscrowID,
		friendlyRoleName(*role),
		*sender,
		input.Decision == DecisionRelease,
		*txID)
	return nil
}

func friendlyRoleName(r uint8) string {
	switch r {
	case 0:
		return "f"
	case 1:
		return "t"
	default:
		return "arb"
	}
}

// GETTERS

//go:wasmexport e_get
func GetEscrow(id *string) *string {
	escrowBase := sdk.StateGetObject(*id)
	if escrowBase == nil || *escrowBase == "" {
		sdk.Abort(fmt.Sprintf("escrow %s not found", *id))
	}
	uintId := StringToUInt64(id)
	escrowParties := loadRoles(uintId)
	am, as := loadReward(uintId)
	escrowDecisions := loadDecisions(uintId)
	c, o := getEscrowOutcome(escrowDecisions)
	escrow := &Escrow{
		ID:   uintId,
		Name: *escrowBase,
		From: EscrowAccount{
			Address:  escrowParties[0],
			Decision: escrowDecisions[0],
		},
		To: EscrowAccount{
			Address:  escrowParties[1],
			Decision: escrowDecisions[1],
		},
		Arbitrator: EscrowAccount{
			Address:  escrowParties[2],
			Decision: escrowDecisions[2],
		},
		Amount:  float64(am) / 1000,
		Asset:   as,
		Closed:  c,
		Outcome: o,
	}

	jsonStr := ToJSON(escrow, "escrow")
	return &jsonStr
}

// STATE PERSISTENCE & LOADING

func saveEscrowBase(escrowID uint64, escrowCsv string) error {
	// Build a proper key for storing the escrow base
	key := strconv.FormatUint(escrowID, 10)

	// Save escrow data
	sdk.StateSetObject(key, escrowCsv)

	// increment
	setEscrowCount(escrowID + 1)

	return nil
}

func saveEscrowParties(escrowID uint64, escrowPartiesCsv string) error {
	// Build a proper key for storing the escrow addresses
	key := strconv.FormatUint(escrowID, 10) + "|p"
	// Save escrow data
	sdk.StateSetObject(key, escrowPartiesCsv)

	return nil
}

func saveEscrowReward(escrowID uint64, amount uint64, asset string) error {
	key := strconv.FormatUint(escrowID, 10) + "|r"
	buf := make([]byte, 0, 32+len(asset))
	buf = strconv.AppendUint(buf, amount, 10)
	buf = append(buf, '|')
	buf = append(buf, asset...)
	sdk.StateSetObject(key, string(buf))
	return nil
}

func saveEscrowDecisions(escrowID uint64, decs []uint8) error {
	// Convert []uint8 to []byte with direct copy (byte is alias of uint8)
	b := make([]byte, len(decs))
	copy(b, decs) // fast native memory copy
	key := strconv.FormatUint(escrowID, 10) + "|d"
	sdk.StateSetObject(key, string(b))
	return nil
}

// HELPERS

func loadRoles(escrowID uint64) []string {
	key := strconv.FormatUint(escrowID, 10) + "|p"
	ptr := sdk.StateGetObject(key)
	if ptr == nil || *ptr == "" {
		sdk.Abort(fmt.Sprintf("parties for escrow %d not found", escrowID))
	}
	data := *ptr
	start := 0
	roles := make([]string, 0, 3)
	for i := 0; i < len(data); i++ {
		if data[i] == '|' {
			roles = append(roles, data[start:i])
			start = i + 1
		}
	}
	roles = append(roles, data[start:])
	if len(roles) != 3 {
		sdk.Abort("invalid parties length")
	}
	return roles
}

func loadDecisions(escrowID uint64) []uint8 {
	key := strconv.FormatUint(escrowID, 10) + "|d"
	ptr := sdk.StateGetObject(key)
	if ptr == nil || *ptr == "" {
		sdk.Abort(fmt.Sprintf("decisions for escrow %d not found", escrowID))
	}
	data := []byte(*ptr)
	if len(data) != 3 {
		sdk.Abort("invalid decisions length")
	}
	decs := make([]uint8, 3)
	copy(decs, data)
	return decs
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

// COMMON HELPERS

func getRoleOfSender(sender *string, parties []string) *uint8 {
	if sender == nil {
		return nil
	}
	for i, p := range parties {
		if p == *sender {
			role := uint8(i) // create a variable
			return &role     // return its pointer
		}
	}
	return nil // not found
}

func getEscrowOutcome(decs []uint8) (bool, uint8) {
	counts := [3]uint8{}
	for _, d := range decs {
		if d > DecisionRelease { // 0..2 only
			sdk.Abort("invalid decision value in state")
		}
		if d != DecisionUnset {
			counts[d]++
			if counts[d] >= 2 {
				return true, d
			}
		}
	}
	return false, DecisionUnset
}

func loadReward(escrowID uint64) (amount uint64, asset string) {
	key := strconv.FormatUint(escrowID, 10) + "|r"
	ptr := sdk.StateGetObject(key)
	if ptr == nil || *ptr == "" {
		sdk.Abort(fmt.Sprintf("amount for escrow %d not found", escrowID))
	}

	return CsvToReward(ptr)

}

func friendlyOutcome(o uint8) string {
	switch o {
	case DecisionRefund:
		return "refund"
	case DecisionRelease:
		return "release"
	default:
		return "pending"
	}
}

func processEscrowOutcome(escrowID uint64, decs []uint8, txId string) {
	c, o := getEscrowOutcome(decs)
	if c {
		am, as := loadReward(escrowID)
		r := loadRoles(escrowID)
		switch o {
		case DecisionRefund:
			receiver := r[0] // escrow creator
			sdk.HiveTransfer(sdk.Address(receiver), int64(am), sdk.Asset(as))
		case DecisionRelease:
			receiver := r[1] // escrow receiver
			sdk.HiveTransfer(sdk.Address(receiver), int64(am), sdk.Asset(as))
		}
		EmitEscrowClosedEvent(escrowID, friendlyOutcome(o), txId)
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

// EscrowID Helpers

func newEscrowID() uint64 {
	ptr := sdk.StateGetObject("cnt:e")
	if ptr == nil || *ptr == "" {
		return 0
	}
	return StringToUInt64(ptr)
}

func setEscrowCount(n uint64) {
	sdk.StateSetObject("cnt:e", strconv.FormatUint(n, 10))
}

// Transfer Allow Intent Helpers

// TransferAllow represents a parsed transfer.allow intent,
// including the limit (amount) and token (asset).
type TransferAllow struct {
	LimitMilli uint64
	Token      sdk.Asset
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
			if !isValidAsset(token) {
				sdk.Abort("invalid intent token")
			}
			limitStr := intent.Args["limit"]
			milli, ok := parseLimitMilli(limitStr)
			if !ok {
				sdk.Abort("invalid intent limit")
			}
			if milli == 0 {
				sdk.Abort("intent >0 needed")
			}
			return &TransferAllow{
				LimitMilli: milli,
				Token:      sdk.Asset(token),
			}
		}
	}
	return nil
}

func parseLimitMilli(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	// no leading +/-, exponent, or spaces allowed
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && c != '.' {
			return 0, false
		}
	}

	var intPart uint64
	var fracPart uint64
	var fracDigits int
	sawDot := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			if sawDot {
				return 0, false
			}
			sawDot = true
			continue
		}
		d := uint64(c - '0')
		if !sawDot {
			// intPart = intPart*10 + d
			// multiply by 10 with overflow check (practically safe for typical limits)
			intPart = intPart*10 + d
			if intPart > ^uint64(0)/1000 {
				return 0, false
			}
		} else if fracDigits < 3 {
			// take up to first 3 fractional digits; floor extra
			fracPart = fracPart*10 + d
			fracDigits++
		} else {
			// extra fractional digits are ignored (floor)
		}
	}

	// pad fractional to 3 digits
	for fracDigits < 3 {
		fracPart *= 10
		fracDigits++
	}

	// milli = intPart*1000 + fracPart
	return intPart*1000 + fracPart, true
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
	emitEvent("cr", map[string]string{
		"id":  strconv.FormatUint(escrowID, 10),
		"f":   fromAddress,
		"t":   toAddress,
		"arb": arbAddress,
		"am":  strconv.FormatFloat(amount, 'f', -1, 64),
		"as":  asset,
	}, txID)
}

// EmitEscrowDecisionEvent emits an event for a new decision.
func EmitEscrowDecisionEvent(escrowID uint64, role string, address string, decision bool, txID string) {
	emitEvent("de", map[string]string{
		"id": strconv.FormatUint(escrowID, 10),
		"r":  role,
		"a":  address,
		"d":  strconv.FormatBool(decision),
	}, txID)
}

// EmitEscrowClosedEvent emits an event for closing an escrow.
func EmitEscrowClosedEvent(escrowID uint64, outcome string, txID string) {
	emitEvent("cl", map[string]string{
		"id": strconv.FormatUint(escrowID, 10),
		"o":  outcome,
	}, txID)
}
