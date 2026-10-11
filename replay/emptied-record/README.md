# emptied-record

> **Synthetic data.** This scenario reconstructs the shape of a public incident using made-up data. None of it is real, and 0cular isn't affiliated with Amazon Web Services.

## The incident

Amazon DynamoDB's DNS management system has a Planner that produces DNS plans, and Enactors that apply them independently in three Availability Zones. On October 19–20, 2025 (US Pacific time), a latent race condition in this automation took DynamoDB's regional endpoint in us-east-1 offline:

1. One Enactor was unusually delayed while it applied an older plan.
2. Meanwhile another Enactor applied a newer plan, then started a cleanup that deletes plans much older than the one it applied.
3. The delayed Enactor's check that its plan was newer than the one in place had run before the delay, and was stale by the time it applied. Its old plan overwrote the newer one.
4. The cleanup then deleted that old plan, which removed every IP address from the record for `dynamodb.us-east-1.amazonaws.com`.

The automation couldn't repair the empty record, and it took manual operator intervention. DynamoDB was impaired from 11:48 PM on October 19 to 2:40 AM on October 20, about 3 hours. The wider us-east-1 impact lasted until 2:20 PM on October 20, about 14.5 hours.

Postmortem: [Summary of the Amazon DynamoDB service disruption in us-east-1](https://aws.amazon.com/message/101925/)

## What's reconstructed

| File | Content |
| --- | --- |
| `baseline.json` | The endpoint's record under the current plan (1042), with six addresses |
| `candidate.json` | The record after a stale plan (1017) replaced the current one and cleanup deleted it: no addresses left |
| `consumer/` | A client stub that picks an address from the record. With none, it fails, like the clients that couldn't reach the endpoint |
| `schema.json` | The record's shape. It doesn't require any addresses at all |
| `contract.yaml` | The contract that would have caught it |
| `expected.yaml` | The expected outcome at each stage |

The endpoint name, plan numbers and addresses are ours: the addresses come from the RFC 5737 documentation ranges, and the name ends in `.example`.

## What catches it

| Stage | Outcome |
| --- | --- |
| Check (v0.1) | BLOCK. The invariant `addresses-never-empty` fails on the empty list |
| Keep (v0.2) | The loader rejects the candidate and keeps the last known good |
| Publish (v0.3) | Publish asserts its parent: a delayed writer whose plan was built on an older version fails the parent check instead of overwriting the newer record. The emptied record itself is blocked by the invariant. The stale plan's data arrives with task 3.6 |

Both files pass `schema.json`: nothing in the record's shape says it can't be empty.

## Run the consumer

```bash
go run ./replay/emptied-record/consumer replay/emptied-record/baseline.json   # prints the address it would use
go run ./replay/emptied-record/consumer replay/emptied-record/candidate.json  # fails: no addresses for kv.region-a.example
```
