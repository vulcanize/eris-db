package state

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"time"

	acm "github.com/eris-ltd/eris-db/account"
	. "github.com/eris-ltd/eris-db/manager/eris-mint/state/types"
	ptypes "github.com/eris-ltd/eris-db/permission/types"
	"github.com/eris-ltd/eris-db/txs"

	. "github.com/tendermint/go-common"
	dbm "github.com/tendermint/go-db"
	"github.com/tendermint/go-events"
	"github.com/tendermint/go-merkle"
	"github.com/tendermint/go-wire"

	core_types "github.com/eris-ltd/eris-db/core/types"
	"github.com/tendermint/tendermint/types"
)

var (
	stateKey                     = []byte("stateKey")
	minBondAmount                = int64(1)           // TODO adjust
	defaultAccountsCacheCapacity = 1000               // TODO adjust
	unbondingPeriodBlocks        = int(60 * 24 * 365) // TODO probably better to make it time based.
	validatorTimeoutBlocks       = int(10)            // TODO adjust
	maxLoadStateElementSize      = 0                  // no max
)

//-----------------------------------------------------------------------------

// NOTE: not goroutine-safe.
type State struct {
	DB              dbm.DB
	ChainID         string
	LastBlockHeight int
	LastBlockHash   []byte
	LastBlockParts  types.PartSetHeader
	LastBlockTime   time.Time
	//	BondedValidators     *types.ValidatorSet
	//	LastBondedValidators *types.ValidatorSet
	//	UnbondingValidators  *types.ValidatorSet
	accounts       merkle.Tree // Shouldn't be accessed directly.
	validatorInfos merkle.Tree // Shouldn't be accessed directly.
	nameReg        merkle.Tree // Shouldn't be accessed directly.

	evc events.Fireable // typically an events.EventCache
}

func LoadState(db dbm.DB) *State {
	s := &State{DB: db}
	buf := db.Get(stateKey)
	if len(buf) == 0 {
		return nil
	} else {
		r, n, err := bytes.NewReader(buf), new(int), new(error)
		s.ChainID = wire.ReadString(r, maxLoadStateElementSize, n, err)
		s.LastBlockHeight = wire.ReadVarint(r, n, err)
		s.LastBlockHash = wire.ReadByteSlice(r, maxLoadStateElementSize, n, err)
		s.LastBlockParts = wire.ReadBinary(types.PartSetHeader{}, r, maxLoadStateElementSize, n, err).(types.PartSetHeader)
		s.LastBlockTime = wire.ReadTime(r, n, err)
		// s.BondedValidators = wire.ReadBinary(&types.ValidatorSet{}, r, maxLoadStateElementSize, n, err).(*types.ValidatorSet)
		// s.LastBondedValidators = wire.ReadBinary(&types.ValidatorSet{}, r, maxLoadStateElementSize, n, err).(*types.ValidatorSet)
		// s.UnbondingValidators = wire.ReadBinary(&types.ValidatorSet{}, r, maxLoadStateElementSize, n, err).(*types.ValidatorSet)
		accountsHash := wire.ReadByteSlice(r, maxLoadStateElementSize, n, err)
		s.accounts = merkle.NewIAVLTree(defaultAccountsCacheCapacity, db)
		s.accounts.Load(accountsHash)
		//validatorInfosHash := wire.ReadByteSlice(r, maxLoadStateElementSize, n, err)
		//s.validatorInfos = merkle.NewIAVLTree(wire.BasicCodec, types.ValidatorInfoCodec, 0, db)
		//s.validatorInfos.Load(validatorInfosHash)
		nameRegHash := wire.ReadByteSlice(r, maxLoadStateElementSize, n, err)
		s.nameReg = merkle.NewIAVLTree(0, db)
		s.nameReg.Load(nameRegHash)
		if *err != nil {
			// DATA HAS BEEN CORRUPTED OR THE SPEC HAS CHANGED
			Exit(Fmt("Data has been corrupted or its spec has changed: %v\n", *err))
		}
		// TODO: ensure that buf is completely read.
	}
	return s
}

func (s *State) Save() {
	s.accounts.Save()
	//s.validatorInfos.Save()
	s.nameReg.Save()
	buf, n, err := new(bytes.Buffer), new(int), new(error)
	wire.WriteString(s.ChainID, buf, n, err)
	wire.WriteVarint(s.LastBlockHeight, buf, n, err)
	wire.WriteByteSlice(s.LastBlockHash, buf, n, err)
	wire.WriteBinary(s.LastBlockParts, buf, n, err)
	wire.WriteTime(s.LastBlockTime, buf, n, err)
	// wire.WriteBinary(s.BondedValidators, buf, n, err)
	// wire.WriteBinary(s.LastBondedValidators, buf, n, err)
	// wire.WriteBinary(s.UnbondingValidators, buf, n, err)
	wire.WriteByteSlice(s.accounts.Hash(), buf, n, err)
	//wire.WriteByteSlice(s.validatorInfos.Hash(), buf, n, err)
	wire.WriteByteSlice(s.nameReg.Hash(), buf, n, err)
	if *err != nil {
		PanicCrisis(*err)
	}
	s.DB.Set(stateKey, buf.Bytes())
}

// CONTRACT:
// Copy() is a cheap way to take a snapshot,
// as if State were copied by value.
func (s *State) Copy() *State {
	return &State{
		DB:              s.DB,
		ChainID:         s.ChainID,
		LastBlockHeight: s.LastBlockHeight,
		LastBlockHash:   s.LastBlockHash,
		LastBlockParts:  s.LastBlockParts,
		LastBlockTime:   s.LastBlockTime,
		// BondedValidators:     s.BondedValidators.Copy(),     // TODO remove need for Copy() here.
		// LastBondedValidators: s.LastBondedValidators.Copy(), // That is, make updates to the validator set
		// UnbondingValidators: s.UnbondingValidators.Copy(), // copy the valSet lazily.
		accounts: s.accounts.Copy(),
		//validatorInfos:       s.validatorInfos.Copy(),
		nameReg: s.nameReg.Copy(),
		evc:     nil,
	}
}

// Returns a hash that represents the state data, excluding Last*
func (s *State) Hash() []byte {
	return merkle.SimpleHashFromMap(map[string]interface{}{
		//"BondedValidators":    s.BondedValidators,
		//"UnbondingValidators": s.UnbondingValidators,
		"Accounts": s.accounts,
		//"ValidatorInfos":      s.validatorInfos,
		"NameRegistry": s.nameReg,
	})
}

/* //XXX Done by tendermint core
// Mutates the block in place and updates it with new state hash.
func (s *State) ComputeBlockStateHash(block *types.Block) error {
	sCopy := s.Copy()
	// sCopy has no event cache in it, so this won't fire events
	err := execBlock(sCopy, block, types.PartSetHeader{})
	if err != nil {
		return err
	}
	// Set block.StateHash
	block.StateHash = sCopy.Hash()
	return nil
}
*/

func (s *State) GetGenesisDoc() (*GenesisDoc, error) {
	var genesisDoc *GenesisDoc
	loadedGenesisDocBytes := s.DB.Get(GenDocKey)
	err := new(error)
	wire.ReadJSONPtr(&genesisDoc, loadedGenesisDocBytes, err)
	if *err != nil {
		return nil, fmt.Errorf("Unable to read genesisDoc from db on Get: %v", err)
	}
	return genesisDoc, nil
}

func (s *State) SetDB(db dbm.DB) {
	s.DB = db
}

//-------------------------------------
// State.params

func (s *State) GetGasLimit() int64 {
	return 1000000 // TODO
}

// State.params
//-------------------------------------
// State.accounts

// Returns nil if account does not exist with given address.
// Implements Statelike
func (s *State) GetAccount(address []byte) *acm.Account {
	_, accBytes, _ := s.accounts.Get(address)
	if accBytes == nil {
		return nil
	}
	return acm.DecodeAccount(accBytes)
}

// The account is copied before setting, so mutating it
// afterwards has no side effects.
// Implements Statelike
func (s *State) UpdateAccount(account *acm.Account) bool {
	return s.accounts.Set(account.Address, acm.EncodeAccount(account))
}

// Implements Statelike
func (s *State) RemoveAccount(address []byte) bool {
	_, removed := s.accounts.Remove(address)
	return removed
}

// The returned Account is a copy, so mutating it
// has no side effects.
func (s *State) GetAccounts() merkle.Tree {
	return s.accounts.Copy()
}

// Set the accounts tree
func (s *State) SetAccounts(accounts merkle.Tree) {
	s.accounts = accounts
}

// State.accounts
//-------------------------------------
// State.validators

// XXX: now handled by tendermint core

/*

// The returned ValidatorInfo is a copy, so mutating it
// has no side effects.
func (s *State) GetValidatorInfo(address []byte) *types.ValidatorInfo {
	_, valInfo := s.validatorInfos.Get(address)
	if valInfo == nil {
		return nil
	}
	return valInfo.(*types.ValidatorInfo).Copy()
}

// Returns false if new, true if updated.
// The valInfo is copied before setting, so mutating it
// afterwards has no side effects.
func (s *State) SetValidatorInfo(valInfo *types.ValidatorInfo) (updated bool) {
	return s.validatorInfos.Set(valInfo.Address, valInfo.Copy())
}

func (s *State) GetValidatorInfos() merkle.Tree {
	return s.validatorInfos.Copy()
}

func (s *State) unbondValidator(val *types.Validator) {
	// Move validator to UnbondingValidators
	val, removed := s.BondedValidators.Remove(val.Address)
	if !removed {
		PanicCrisis("Couldn't remove validator for unbonding")
	}
	val.UnbondHeight = s.LastBlockHeight + 1
	added := s.UnbondingValidators.Add(val)
	if !added {
		PanicCrisis("Couldn't add validator for unbonding")
	}
}

func (s *State) rebondValidator(val *types.Validator) {
	// Move validator to BondingValidators
	val, removed := s.UnbondingValidators.Remove(val.Address)
	if !removed {
		PanicCrisis("Couldn't remove validator for rebonding")
	}
	val.BondHeight = s.LastBlockHeight + 1
	added := s.BondedValidators.Add(val)
	if !added {
		PanicCrisis("Couldn't add validator for rebonding")
	}
}

func (s *State) releaseValidator(val *types.Validator) {
	// Update validatorInfo
	valInfo := s.GetValidatorInfo(val.Address)
	if valInfo == nil {
		PanicSanity("Couldn't find validatorInfo for release")
	}
	valInfo.ReleasedHeight = s.LastBlockHeight + 1
	s.SetValidatorInfo(valInfo)

	// Send coins back to UnbondTo outputs
	accounts, err := getOrMakeOutputs(s, nil, valInfo.UnbondTo)
	if err != nil {
		PanicSanity("Couldn't get or make unbondTo accounts")
	}
	adjustByOutputs(accounts, valInfo.UnbondTo)
	for _, acc := range accounts {
		s.UpdateAccount(acc)
	}

	// Remove validator from UnbondingValidators
	_, removed := s.UnbondingValidators.Remove(val.Address)
	if !removed {
		PanicCrisis("Couldn't remove validator for release")
	}
}

func (s *State) destroyValidator(val *types.Validator) {
	// Update validatorInfo
	valInfo := s.GetValidatorInfo(val.Address)
	if valInfo == nil {
		PanicSanity("Couldn't find validatorInfo for release")
	}
	valInfo.DestroyedHeight = s.LastBlockHeight + 1
	valInfo.DestroyedAmount = val.VotingPower
	s.SetValidatorInfo(valInfo)

	// Remove validator
	_, removed := s.BondedValidators.Remove(val.Address)
	if !removed {
		_, removed := s.UnbondingValidators.Remove(val.Address)
		if !removed {
			PanicCrisis("Couldn't remove validator for destruction")
		}
	}

}

// Set the validator infos tree
func (s *State) SetValidatorInfos(validatorInfos merkle.Tree) {
	s.validatorInfos = validatorInfos
}

*/

// State.validators
//-------------------------------------
// State.storage

func (s *State) LoadStorage(hash []byte) (storage merkle.Tree) {
	storage = merkle.NewIAVLTree(1024, s.DB)
	storage.Load(hash)
	return storage
}

// State.storage
//-------------------------------------
// State.nameReg

func (s *State) GetNameRegEntry(name string) *core_types.NameRegEntry {
	_, valueBytes, _ := s.nameReg.Get([]byte(name))
	if valueBytes == nil {
		return nil
	}

	return DecodeNameRegEntry(valueBytes)
}

func DecodeNameRegEntry(entryBytes []byte) *core_types.NameRegEntry {
	var n int
	var err error
	value := NameRegCodec.Decode(bytes.NewBuffer(entryBytes), &n, &err)
	return value.(*core_types.NameRegEntry)
}

func (s *State) UpdateNameRegEntry(entry *core_types.NameRegEntry) bool {
	w := new(bytes.Buffer)
	var n int
	var err error
	NameRegCodec.Encode(entry, w, &n, &err)
	return s.nameReg.Set([]byte(entry.Name), w.Bytes())
}

func (s *State) RemoveNameRegEntry(name string) bool {
	_, removed := s.nameReg.Remove([]byte(name))
	return removed
}

func (s *State) GetNames() merkle.Tree {
	return s.nameReg.Copy()
}

// Set the name reg tree
func (s *State) SetNameReg(nameReg merkle.Tree) {
	s.nameReg = nameReg
}

func NameRegEncoder(o interface{}, w io.Writer, n *int, err *error) {
	wire.WriteBinary(o.(*core_types.NameRegEntry), w, n, err)
}

func NameRegDecoder(r io.Reader, n *int, err *error) interface{} {
	return wire.ReadBinary(&core_types.NameRegEntry{}, r, txs.MaxDataLength, n, err)
}

var NameRegCodec = wire.Codec{
	Encode: NameRegEncoder,
	Decode: NameRegDecoder,
}

// State.nameReg
//-------------------------------------

// Implements events.Eventable. Typically uses events.EventCache
func (s *State) SetFireable(evc events.Fireable) {
	s.evc = evc
}

//-----------------------------------------------------------------------------
// Genesis

func MakeGenesisStateFromFile(db dbm.DB, genDocFile string) (*GenesisDoc, *State) {
	jsonBlob, err := ioutil.ReadFile(genDocFile)
	if err != nil {
		Exit(Fmt("Couldn't read GenesisDoc file: %v", err))
	}
	genDoc := GenesisDocFromJSON(jsonBlob)
	return genDoc, MakeGenesisState(db, genDoc)
}

func MakeGenesisState(db dbm.DB, genDoc *GenesisDoc) *State {
	if len(genDoc.Validators) == 0 {
		Exit(Fmt("The genesis file has no validators"))
	}

	if genDoc.GenesisTime.IsZero() {
		// MARMOT: really?
		// set time to 11/18/2016 @ 4:09am (UTC)
		genDoc.GenesisTime = time.Unix(1479442162, 0)
		// genDoc.GenesisTime = time.Now()
	}

	// Make accounts state tree
	accounts := merkle.NewIAVLTree(defaultAccountsCacheCapacity, db)
	for _, genAcc := range genDoc.Accounts {
		perm := ptypes.ZeroAccountPermissions
		if genAcc.Permissions != nil {
			perm = *genAcc.Permissions
		}
		acc := &acm.Account{
			Address:     genAcc.Address,
			PubKey:      nil,
			Sequence:    0,
			Balance:     genAcc.Amount,
			Permissions: perm,
		}
		accounts.Set(acc.Address, acm.EncodeAccount(acc))
	}

	// global permissions are saved as the 0 address
	// so they are included in the accounts tree
	globalPerms := ptypes.DefaultAccountPermissions
	if genDoc.Params != nil && genDoc.Params.GlobalPermissions != nil {
		globalPerms = *genDoc.Params.GlobalPermissions
		// XXX: make sure the set bits are all true
		// Without it the HasPermission() functions will fail
		globalPerms.Base.SetBit = ptypes.AllPermFlags
	}

	permsAcc := &acm.Account{
		Address:     ptypes.GlobalPermissionsAddress,
		PubKey:      nil,
		Sequence:    0,
		Balance:     1337,
		Permissions: globalPerms,
	}
	accounts.Set(permsAcc.Address, acm.EncodeAccount(permsAcc))

	// Make validatorInfos state tree && validators slice
	/*
		validatorInfos := merkle.NewIAVLTree(wire.BasicCodec, types.ValidatorInfoCodec, 0, db)
		validators := make([]*types.Validator, len(genDoc.Validators))
		for i, val := range genDoc.Validators {
			pubKey := val.PubKey
			address := pubKey.Address()

			// Make ValidatorInfo
			valInfo := &types.ValidatorInfo{
				Address:         address,
				PubKey:          pubKey,
				UnbondTo:        make([]*types.TxOutput, len(val.UnbondTo)),
				FirstBondHeight: 0,
				FirstBondAmount: val.Amount,
			}
			for i, unbondTo := range val.UnbondTo {
				valInfo.UnbondTo[i] = &types.TxOutput{
					Address: unbondTo.Address,
					Amount:  unbondTo.Amount,
				}
			}
			validatorInfos.Set(address, valInfo)

			// Make validator
			validators[i] = &types.Validator{
				Address:     address,
				PubKey:      pubKey,
				VotingPower: val.Amount,
			}
		}
	*/

	// Make namereg tree
	nameReg := merkle.NewIAVLTree(0, db)
	// TODO: add names, contracts to genesis.json

	// IAVLTrees must be persisted before copy operations.
	accounts.Save()
	//validatorInfos.Save()
	nameReg.Save()

	return &State{
		DB:              db,
		ChainID:         genDoc.ChainID,
		LastBlockHeight: 0,
		LastBlockHash:   nil,
		LastBlockParts:  types.PartSetHeader{},
		LastBlockTime:   genDoc.GenesisTime,
		//BondedValidators:     types.NewValidatorSet(validators),
		//LastBondedValidators: types.NewValidatorSet(nil),
		//UnbondingValidators:  types.NewValidatorSet(nil),
		accounts: accounts,
		//validatorInfos:       validatorInfos,
		nameReg: nameReg,
	}
}
