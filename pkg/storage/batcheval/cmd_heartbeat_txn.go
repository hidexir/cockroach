// Copyright 2014 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

package batcheval

import (
	"context"
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/keys"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/storage/batcheval/result"
	"github.com/cockroachdb/cockroach/pkg/storage/engine"
	"github.com/cockroachdb/cockroach/pkg/storage/spanset"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
)

func init() {
	RegisterCommand(roachpb.HeartbeatTxn, declareKeysHeartbeatTransaction, HeartbeatTxn)
}

func declareKeysHeartbeatTransaction(
	desc roachpb.RangeDescriptor, header roachpb.Header, req roachpb.Request, spans *spanset.SpanSet,
) {
	declareKeysWriteTransaction(desc, header, req, spans)
}

// HeartbeatTxn updates the transaction status and heartbeat
// timestamp after receiving transaction heartbeat messages from
// coordinator. Returns the updated transaction.
func HeartbeatTxn(
	ctx context.Context, batch engine.ReadWriter, cArgs CommandArgs, resp roachpb.Response,
) (result.Result, error) {
	args := cArgs.Args.(*roachpb.HeartbeatTxnRequest)
	h := cArgs.Header
	reply := resp.(*roachpb.HeartbeatTxnResponse)

	if err := VerifyTransaction(h, args); err != nil {
		return result.Result{}, err
	}

	if args.Now.IsEmpty() {
		return result.Result{}, fmt.Errorf("Now not specified for heartbeat")
	}

	key := keys.TransactionKey(h.Txn.Key, h.Txn.ID)

	var txn roachpb.Transaction
	if ok, err := engine.MVCCGetProto(
		ctx, batch, key, hlc.Timestamp{}, &txn, engine.MVCCGetOptions{},
	); err != nil {
		return result.Result{}, err
	} else if !ok {
		// No existing transaction record was found - create one by writing
		// it below.
		txn = h.Txn.Clone()
		if txn.Status != roachpb.PENDING {
			return result.Result{}, roachpb.NewTransactionStatusError(
				fmt.Sprintf("cannot heartbeat txn with status %v: %s", txn.Status, txn),
			)
		}

		// Verify that it is safe to create the transaction record.
		if ok, reason := cArgs.EvalCtx.CanCreateTxnRecord(&txn); !ok {
			return result.Result{}, roachpb.NewTransactionAbortedError(reason)
		}
	}

	if txn.Status == roachpb.PENDING {
		txn.LastHeartbeat.Forward(args.Now)
		txnRecord := txn.AsRecord()
		if err := engine.MVCCPutProto(ctx, batch, cArgs.Stats, key, hlc.Timestamp{}, nil, &txnRecord); err != nil {
			return result.Result{}, err
		}
	}

	reply.Txn = &txn
	return result.Result{}, nil
}
