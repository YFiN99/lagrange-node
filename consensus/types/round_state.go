package types

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/Lagrange-Labs/lagrange-node/crypto"
	"github.com/Lagrange-Labs/lagrange-node/logger"
	sequencerv2types "github.com/Lagrange-Labs/lagrange-node/sequencer/types/v2"
	"github.com/Lagrange-Labs/lagrange-node/telemetry"
	"github.com/Lagrange-Labs/lagrange-node/utils"
)

// RoundState defines the internal consensus state.
type RoundState struct {
	blsScheme     crypto.BLSScheme
	proposedBatch *sequencerv2types.Batch

	commitSignatures map[string]map[string]*sequencerv2types.BlsSignature
	evidences        []*sequencerv2types.BlsSignature // to determine slashing

	rwMutex   sync.RWMutex // to protect the round state updates
	isBlocked bool         // to prevent the block commit
}

// NewEmptyRoundState creates a new empty round state for rwMutex.
func NewEmptyRoundState(blsScheme crypto.BLSScheme) *RoundState {
	return &RoundState{
		isBlocked: true,
		blsScheme: blsScheme,
	}
}

// UpdateRoundState updates a new round state.
func (rs *RoundState) UpdateRoundState(prposedBatch *sequencerv2types.Batch) {
	rs.rwMutex.Lock()
	defer rs.rwMutex.Unlock()

	rs.proposedBatch = prposedBatch
	rs.commitSignatures = make(map[string]map[string]*sequencerv2types.BlsSignature)
	rs.isBlocked = false
}

// AddCommit adds a commit to the round state.
func (rs *RoundState) AddCommit(commit *sequencerv2types.BlsSignature, pubKey string, stakeAddr string) error {
	rs.rwMutex.Lock()
	defer rs.rwMutex.Unlock()

	if rs.isBlocked {
		return fmt.Errorf("the current round is blocked")
	}
	if _, ok := rs.commitSignatures[stakeAddr]; !ok {
		rs.commitSignatures[stakeAddr] = make(map[string]*sequencerv2types.BlsSignature)
	}
	rs.commitSignatures[stakeAddr][pubKey] = commit

	return nil
}

// BlockCommit blocks adds a commit to the round state.
func (rs *RoundState) BlockCommit() {
	rs.rwMutex.Lock()
	defer rs.rwMutex.Unlock()

	rs.isBlocked = true
}

// UnblockCommit unblocks adds a commit to the round state.
func (rs *RoundState) UnblockCommit() {
	rs.rwMutex.Lock()
	defer rs.rwMutex.Unlock()

	rs.isBlocked = false
}

// IsFinalized checks if the block is finalized.
func (rs *RoundState) IsFinalized() bool {
	rs.rwMutex.RLock()
	defer rs.rwMutex.RUnlock()

	if rs.proposedBatch == nil {
		return false
	}
	return len(rs.proposedBatch.PubKeys) > 0
}

// GetCurrentBatchNumber returns the current batch number.
func (rs *RoundState) GetCurrentBatchNumber() uint64 {
	rs.rwMutex.RLock()
	defer rs.rwMutex.RUnlock()

	if rs.proposedBatch == nil {
		return 0
	}
	return rs.proposedBatch.BatchNumber()
}

// GetCurrentBatch returns the current batch.
func (rs *RoundState) GetCurrentBatch() *sequencerv2types.Batch {
	rs.rwMutex.RLock()
	defer rs.rwMutex.RUnlock()

	return rs.proposedBatch
}

// CheckEnoughVotingPower checks if there is enough voting power to finalize the block.
func (rs *RoundState) CheckEnoughVotingPower(vs *ValidatorSet) bool {
	rs.rwMutex.RLock()
	defer rs.rwMutex.RUnlock()

	votingPower := uint64(0)
	votingCount := 0
	for stakeAddr, signatures := range rs.commitSignatures {
		for pubKey := range signatures {
			votingPower += vs.GetVotingPower(stakeAddr, pubKey)
			votingCount++
		}
	}

	logger.Infof("committed count: %d, committed voting power: %v, total voting power: %v", votingCount, votingPower, vs.GetCommitteeVotingPower())

	result := votingCount*3 > vs.GetValidatorCount() && votingPower*3 > vs.GetCommitteeVotingPower()*2
	if !result {
		telemetry.SetGauge(float64(vs.GetValidatorCount()-votingCount), "consensus", "missing_count")
	}
	telemetry.AddSample(float32(votingPower)/float32(vs.GetCommitteeVotingPower()), "consensus", "committed_voting_power_ratio")

	return result
}

// CheckAggregatedSignature checks if the aggregated signature is valid.
func (rs *RoundState) CheckAggregatedSignature() error {
	rs.rwMutex.Lock()
	defer rs.rwMutex.Unlock()

	if rs.proposedBatch == nil {
		return fmt.Errorf("the proposed batch is nil")
	}

	blsSignature := rs.proposedBatch.BlsSignature()
	sigHash := blsSignature.Hash()
	signatures := make([][]byte, 0)
	pubKeys := make([][]byte, 0)

	// aggregate the signatures of client nodes
	for _, operatorSignatures := range rs.commitSignatures {
		for pubKey, commit := range operatorSignatures {
			signatures = append(signatures, utils.Hex2Bytes(commit.BlsSignature))
			pubKeys = append(pubKeys, utils.Hex2Bytes(pubKey))
		}
	}

	aggSig, err := rs.blsScheme.AggregateSignatures(signatures)
	if err != nil {
		logger.Errorf("failed to aggregate the signatures: %v", err)
	} else {
		verified, err := rs.blsScheme.VerifyAggregatedSignature(pubKeys, sigHash, aggSig)
		if err == nil && verified {
			rs.proposedBatch.AggSignature = utils.Bytes2Hex(aggSig)
			for _, pubKey := range pubKeys {
				rs.proposedBatch.PubKeys = append(rs.proposedBatch.PubKeys, utils.Bytes2Hex(pubKey))
			}
			return nil
		}
		if err != nil {
			logger.Errorf("failed to verify the aggregated signature: %v", err)
		}
	}

	// find the invalid signature
	for operator, operatorSignatures := range rs.commitSignatures {
		for pubKey, commit := range operatorSignatures {
			commitHash := commit.Hash()
			if !bytes.Equal(commitHash, sigHash) {
				logger.Errorf("wrong commit message: %v, original: %v", utils.Bytes2Hex(commitHash), utils.Bytes2Hex(sigHash))
				rs.addEvidence(operator, commit)
				continue
			}
			verified, err := rs.blsScheme.VerifySignature(utils.Hex2Bytes(pubKey), commitHash, utils.Hex2Bytes(commit.BlsSignature))
			if err != nil {
				logger.Errorf("failed to verify the signature: %v", err)
				rs.addEvidence(operator, commit)
				continue
			}
			if !verified {
				logger.Errorf("invalid signature: %v", commit)
				rs.addEvidence(operator, commit)
			}
		}
	}

	logger.Errorf("invalid aggregated signature: %v", rs.proposedBatch)

	return ErrInvalidAggregativeSignature
}

func (rs *RoundState) addEvidence(operator string, signature *sequencerv2types.BlsSignature) {
	rs.evidences = append(rs.evidences, signature)
	delete(rs.commitSignatures, operator)
}

// GetEvidences returns the evidences.
func (rs *RoundState) GetEvidences() ([]*Evidence, error) {
	rs.rwMutex.RLock()
	defer rs.rwMutex.RUnlock()
	var evidences []*Evidence

	for _, req := range rs.evidences {
		evidence, err := GetEvidence(req)
		if err != nil {
			return nil, err
		}
		evidences = append(evidences, evidence)
	}
	return evidences, nil
}

var (
	ErrInvalidAggregativeSignature = fmt.Errorf("invalid aggregative signature")
)
