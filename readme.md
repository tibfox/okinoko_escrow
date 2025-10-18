# Okinoko Escrow Smart Contract v3

This repository contains a **smart contract written in Go** for the [vsc ecosystem](https://github.com/vsc-eco/).
The contract provides an **on-chain escrow mechanism** between two parties with an **optional third-party arbitrator** to resolve disputes.

## 📖 Overview

* **Language:** Go (Golang) 1.24.0
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

@bob sends the following `custom_json` transaction (id = "vsc.call") via Hive Keychain or [Ōkinoko Terminal](https://terminal.okinoko.io/), signing with his **active key**:

```json
{
  "net_id": "vsc-mainnet",
  "contract_id": "vsc1BonkE2CtHqjnkFdH8hoAEMP25bbWhSr3UA",
  "action": "e_create",
  "payload": "Creating My Website|hive:alice|hive:carol",
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
  "payload": "123|r",
  "intents": [],
  "rcLimit": 10000
}
```

This indicates that she believes the project has been successfully completed (`escrow id = 123` & `decision = r`).

#### 🙅 Step 3: Bob disagrees

@bob decides that an additional feature is required and refuses to release the funds. He submits the following transaction:

```json
{
  "net_id": "vsc-mainnet",
  "contract_id": "vsc1BonkE2CtHqjnkFdH8hoAEMP25bbWhSr3UA",
  "action": "e_decide",
  "payload": "123|f",
  "intents": [],
  "rcLimit": 10000
}
```

This means @bob does **not** agree to release the funds yet (`decision = false`).

#### ⚖️ Step 4: Arbitration and final decision

@alice contacts @carol, the arbitrator. After reviewing the situation, @carol agrees that @alice fulfilled the requirements. She then signs the same decision:

```json
{
  "net_id": "vsc-mainnet",
  "contract_id": "vsc1BonkE2CtHqjnkFdH8hoAEMP25bbWhSr3UA",
  "action": "e_decide",
  "payload": "123|r",
  "intents": [],
  "rcLimit": 10000
}
```

Because both **@alice and @carol** voted `decision = true`, the contract automatically releases the 100 HBD to @alice.

#### ✅ Result

The funds are successfully transferred to **@alice**, completing the escrow in a transparent and decentralized way without relying on a centralized intermediary.

## 📖 Schema

Each escrow instance represents a single conditional transaction between three participants:

* **From (Sender):** Initiates the escrow and funds it.
* **To (Receiver):** Recipient of the funds upon release.
* **Arbitrator:** Neutral third party resolving conflicts.

Each escrow can be in one of two terminal states:

| Closed    | Description                       | Outcome                                         |
| -------- | --------------------------------- | ----------------------------------------------- |
| `false`   | Awaiting decisions                | `p` (pending)                                        | 
| `true` | Finalized after majority decision | `r` (release to receiver) or `f` (refund to sender) |

### Example

```
Escrow #42
├── Name: "Design Project Payment"
├── From: hive:client1 & decision 
├── To: hive:freelancer2 & decision
├── Arbitrator: hive:escrowhub & decision
├── Amount: 100.000 HBD
├── Outcome: pending
└── Closed: false
```

## 📖 Exported Functions

Below you’ll find all exported functions usable via [Ōkinoko Terminal](https://terminal.okinoko.io/) or manually via [Hive Keychain Playground](https://play.hive-keychain.com/#/request/custom).

### 🏗️ Mutations

#### Create Escrow

**Action:** `e_create`

Creates a new escrow contract between sender, receiver, and arbitrator.

**Payload:**

```json5
"Design Project|hive:freelancer2|hive:escrowhub"
```

**Required Intent:**
A valid `transfer.allow` intent must be included in the transaction
(e.g., allow 100 HBD to be held in escrow).

#### Add Decision

**Action:** `e_decide`

Adds a decision (`r` for release or `f` for refund) from one of the escrow participants.

**Payload:**

```json5
"42|r"
```

Each participant (`from`, `to`, or `arbitrator`) may submit one decision. The decision can be changed until escrow is closed.

When two matching decisions exist:

* `r` → funds released to receiver
* `f` → funds refunded to sender

### 🔍 Queries

#### Get Escrow

**Action:** `e_get`

Retrieves a full escrow object by ID.

| Parameter | Type   | Description |
| --------- | ------ | ----------- |
| `id`      | string | Escrow ID   |

**Example Payload:**

`"42"`

**Response:**

```json5
{
  "id": 42, // escrow ID
  "n": "Design Project", // name
  "f": {"a": "hive:client1", "d": "p"}, // from (address, decision)
  "t": {"a": "hive:freelancer2", "d": "r"}, // to (address, decision)
  "arb": {"a": "hive:escrowhub", "d": "r"}, // arbitrator (address, decision)
  "am": 100.0, // amount
  "as": "HBD", // asset
  "cl": true, // closed
  "o": "r" // outcome (r=release / f=refund)
}
```

## 🔔 On-Chain Events

The contract is not designed for "easy" querrying via the standard api node graphql endpoint. 
Instead writing additional contract state keys, each function emits standardized event logs for indexers and user interfaces.
All events are structured as JSON objects with `type`, `attributes`, and `tx` fields.


#### 🧾 Escrow Created Event

```json5
{
  "type": "cr",
  "attributes": {
    "id": "42", // escrow id
    "f": "hive:client1", // from
    "t": "hive:freelancer2", // to
    "arb": "hive:escrowhub", // arbitrator
    "am": "100.000", // amount
    "as": "HBD" // asset
  },
  "tx": "txId of creation"
}
```

#### 🗳️ Decision Event

```json5
{
  "type": "de",
  "attributes": {
    "id": "42", // escrow id
    "r": "t", // role (f=From / t=to / arb=arbitrator)
    "a": "hive:freelancer2", // address
    "d": "r" // decision (r=release / f=refund)
  },
  "tx": "txId of decision"
}
```

#### ✅ Escrow Closed Event

```json5
{
  "type": "cl",
  "attributes": {
    "id": "42", // escrow id
    "o": "r" // final outcome (r=release / f=refund)
  },
  "tx": "txId of resolving decision"
}
```

## 📜 License

This project is licensed under the [MIT License](LICENSE).