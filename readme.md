# Okinoko Escrow Smart Contract

This repository contains a **smart contract written in Go** for the [vsc ecosystem](https://github.com/vsc-eco/).
The contract provides an **on-chain escrow mechanism** between two parties with an **optional third-party arbitrator** to resolve disputes.

## 📖 Overview

* **Language:** Go (Golang) 1.23.2+
* **Purpose:** Provides functions to create and handle escrow agreements between Hive users.
* **Core Features:**
  * On-chain creation of escrows via *transfer allow* intents
  * Multi-party decision model (sender, receiver, arbitrator)
  * Automatic payout to receiver or refund to sender based on majority decision
  * Event-based transparency for future off-chain indexers

## Real-Life Example: Freelance Escrow on Hive

@bob wants to hire @alice to **create his website**, with a payment of **100 HBD** once the site is complete and meets all requirements. To ensure fairness, they agree to use a **VSC escrow contract**.

For security, @bob asks @carol (who is not best buddies with either of them and has a good reputation) to act as the **arbitrator** in case @bob and @alice cannot agree on the project outcome.

#### 🧾 Step 1: Bob creates the escrow

@bob sends the following `custom_json` transaction (id = "vsc.call") via Hive Keychain, signing with his **active key**:

```json
{
  "net_id": "vsc-mainnet",
  "contract_id": "vsc1BonkE2CtHqjnkFdH8hoAEMP25bbWhSr3UA",
  "action": "e_create",
  "payload": "{\"name\":\"Creating My Website\",\"to\":\"hive:alice\",\"arb\":\"hive:carol\"}",
  "intents": [
    {
      "type": "transfer.allow",
      "args": {
        "limit": "100",
        "token": "hbd"
      }
    }
  ],
  "rcLimit": 10000
}
```

This creates an escrow instance named **"Creating My Website"**, draws **100 HBD** from @bob's vsc wallet and locks them in the contract. @Bob receives as output the escrow id `123` of the instance so all parties can refer to that in upcoming decisions.

#### 👩‍💻 Step 2: Alice completes the work

After finishing the website, @alice checks all agreed-upon requirements and reports to @bob. She then signs and sends:

```json
{
  "net_id": "vsc-mainnet",
  "contract_id": "vsc1BonkE2CtHqjnkFdH8hoAEMP25bbWhSr3UA",
  "action": "e_decide",
  "payload": "{\"id\":123,\"d\":true}",
  "intents": [],
  "rcLimit": 10000
}
```

This indicates that she believes the project has been successfully completed (`escrow id = 123` & `d = true`).

#### 🙅 Step 3: Bob disagrees

@bob decides that an additional feature is required and refuses to release the funds. He submits the following transaction:

```json
{
  "net_id": "vsc-mainnet",
  "contract_id": "vsc1BonkE2CtHqjnkFdH8hoAEMP25bbWhSr3UA",
  "action": "e_decide",
  "payload": "{\"id\":123,\"d\":false}",
  "intents": [],
  "rcLimit": 10000
}
```

This means @bob does **not** agree to release the funds yet (`d = false`).

#### ⚖️ Step 4: Arbitration and final decision

@alice contacts @carol, the arbitrator. After reviewing the situation, @carol agrees that @alice fulfilled the requirements. She then signs the same decision:

```json
{
  "net_id": "vsc-mainnet",
  "contract_id": "vsc1BonkE2CtHqjnkFdH8hoAEMP25bbWhSr3UA",
  "action": "e_decide",
  "payload": "{\"id\":123,\"d\":true}",
  "intents": [],
  "rcLimit": 10000
}
```

Because both **@alice and @carol** voted `d = true`, the contract automatically releases the 100 HBD to @alice.

#### ✅ Result

The funds are successfully transferred to **@alice**, completing the escrow in a transparent and decentralized way without relying on a centralized intermediary.

## 📖 Schema

Each escrow instance represents a single conditional transaction between three participants:

* **From (Sender):** Initiates the escrow and funds it.
* **To (Receiver):** Recipient of the funds upon release.
* **Arbitrator:** Neutral third party resolving conflicts.

Each escrow can be in one of two terminal states:

| State    | Description                       | Outcome                                         |
| -------- | --------------------------------- | ----------------------------------------------- |
| `open`   | Awaiting decisions                | Pending                                         |
| `closed` | Finalized after majority decision | `release` (to receiver) or `refund` (to sender) |

### Example

```
Escrow #42
├── Name: "Design Project Payment"
├── From: hive:client1
├── To: hive:freelancer2
├── Arbitrator: hive:escrowhub
├── Amount: 100.000 HBD
├── Outcome: pending
└── Closed: false
```

## 📖 Exported Functions

Below you’ll find all exported functions usable via [Hive Keychain Playground](https://play.hive-keychain.com/#/request/custom) or through the (upcoming) okinoko terminal.

### 🏗️ Mutations

#### Create Escrow

**Action:** `e_create`

Creates a new escrow contract between sender, receiver, and arbitrator.

**Payload:**

```json5
{
  "name": "Design Project",      // mandatory: escrow name (max 100 chars)
  "to": "hive:freelancer2",      // mandatory: receiver address
  "arb": "hive:escrowhub"        // mandatory: arbitrator address
}
```

**Required Intent:**
A valid `transfer.allow` intent must be included in the transaction
(e.g., allow 100 HBD to be held in escrow).

#### Add Decision

**Action:** `e_decide`

Adds a decision (approve or deny release) from one of the escrow participants.

**Payload:**

```json5
{
  "id": 42,                      // mandatory: escrow ID
  "d": true                      // mandatory: decision (true = release / false = refund)
}
```

Each participant (`from`, `to`, or `arb`) may submit one decision. The decision can be changed until escrow is closed.

When two matching decisions exist:

* `true` → funds released to receiver
* `false` → funds refunded to sender

### 🔍 Queries

#### Get Escrow

**Action:** `e_get`

Retrieves a full escrow object by ID.

| Parameter | Type   | Description |
| --------- | ------ | ----------- |
| `id`      | string | Escrow ID   |

**Example Payload:**

```
"42"
```

**Response:**

```json
{
  "id": 42,
  "name": "Design Project",
  "from": {"address": "hive:client1", "agree": null},
  "to": {"address": "hive:freelancer2", "agree": true},
  "arb": {"address": "hive:escrowhub", "agree": true},
  "amount": 100.0,
  "asset": "HBD",
  "closed": true,
  "outcome": "release"
}
```

## 🔔 On-Chain Events

Each function emits standardized event logs for indexers and user interfaces.
All events are structured as JSON objects with `type`, `attributes`, and `tx` fields.

### Event Summary Table

| Event Type        | Key Attributes                               | Description                   |
| ----------------- | -------------------------------------------- | ----------------------------- |
| `escrow_created`  | `id`, `from`, `to`, `arb`, `amount`, `asset` | New escrow created            |
| `escrow_decision` | `id`, `byRole`, `byAddress`, `decision`      | A participant made a decision |
| `escrow_closed`   | `id`, `outcome`                              | Escrow finalized              |

### Event Examples

#### 🧾 Escrow Created Event

```json
{
  "type": "escrow_created",
  "attributes": {
    "id": "42",
    "from": "hive:client1",
    "to": "hive:freelancer2",
    "arb": "hive:escrowhub",
    "amount": "100.000",
    "asset": "HBD"
  },
  "tx":"txId of creation"
}
```

#### 🗳️ Decision Event

```json
{
  "type": "escrow_decision",
  "attributes": {
    "id": "42",
    "byRole": "To",
    "byAddress": "hive:freelancer2",
    "decision": "true"
  },
  "tx":"txId of decision"
}
```

#### ✅ Escrow Closed Event

```json
{
  "type": "escrow_closed",
  "attributes": {
    "id": "42",
    "outcome": "release"
  },
  "tx":"txId of resolving decision"
}
```

## 📜 License
This project is licensed under the [MIT License](LICENSE).