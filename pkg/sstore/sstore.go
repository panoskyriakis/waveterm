package sstore

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql/driver"
	"fmt"
	"log"
	"os"
	"os/user"
	"path"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/scripthaus-dev/mshell/pkg/base"
	"github.com/scripthaus-dev/mshell/pkg/packet"
	"github.com/scripthaus-dev/sh2-server/pkg/scbase"

	_ "github.com/mattn/go-sqlite3"
)

const LineTypeCmd = "cmd"
const LineTypeText = "text"
const DBFileName = "sh2.db"

const DefaultSessionName = "default"
const DefaultWindowName = "default"
const LocalRemoteName = "local"
const DefaultScreenWindowName = "w1"

const DefaultCwd = "~"

const (
	CmdStatusRunning  = "running"
	CmdStatusDetached = "detached"
	CmdStatusError    = "error"
	CmdStatusDone     = "done"
	CmdStatusHangup   = "hangup"
)

const (
	ShareModeLocal   = "local"
	ShareModePrivate = "private"
	ShareModeView    = "view"
	ShareModeShared  = "shared"
)

var globalDBLock = &sync.Mutex{}
var globalDB *sqlx.DB
var globalDBErr error

func GetSessionDBName() string {
	scHome := scbase.GetScHomeDir()
	return path.Join(scHome, DBFileName)
}

func GetDB(ctx context.Context) (*sqlx.DB, error) {
	if IsTxWrapContext(ctx) {
		return nil, fmt.Errorf("cannot call GetDB from within a running transaction")
	}
	globalDBLock.Lock()
	defer globalDBLock.Unlock()
	if globalDB == nil && globalDBErr == nil {
		dbName := GetSessionDBName()
		globalDB, globalDBErr = sqlx.Open("sqlite3", fmt.Sprintf("file:%s?cache=shared&mode=rwc&_journal_mode=WAL&_busy_timeout=5000", dbName))
		if globalDBErr != nil {
			globalDBErr = fmt.Errorf("opening db[%s]: %w", dbName, globalDBErr)
		}
	}
	return globalDB, globalDBErr
}

type UserData struct {
	UserId              string `json:"userid"`
	UserPrivateKeyBytes []byte `json:"-"`
	UserPublicKeyBytes  []byte `json:"-"`
	UserPrivateKey      *ecdsa.PrivateKey
	UserPublicKey       *ecdsa.PublicKey
}

type SessionType struct {
	SessionId      string            `json:"sessionid"`
	Name           string            `json:"name"`
	SessionIdx     int64             `json:"sessionidx"`
	ActiveScreenId string            `json:"activescreenid"`
	OwnerUserId    string            `json:"owneruserid"`
	ShareMode      string            `json:"sharemode"`
	AccessKey      string            `json:"-"`
	NotifyNum      int64             `json:"notifynum"`
	Screens        []*ScreenType     `json:"screens"`
	Remotes        []*RemoteInstance `json:"remotes"`

	// only for updates
	Remove bool `json:"remove,omitempty"`
	Full   bool `json:"full,omitempty"`
}

type WindowOptsType struct {
}

func (opts *WindowOptsType) Scan(val interface{}) error {
	return quickScanJson(opts, val)
}

func (opts WindowOptsType) Value() (driver.Value, error) {
	return quickValueJson(opts)
}

type WindowShareOptsType struct {
}

func (opts *WindowShareOptsType) Scan(val interface{}) error {
	return quickScanJson(opts, val)
}

func (opts WindowShareOptsType) Value() (driver.Value, error) {
	return quickValueJson(opts)
}

type WindowType struct {
	SessionId   string              `json:"sessionid"`
	WindowId    string              `json:"windowid"`
	CurRemote   string              `json:"curremote"`
	WinOpts     WindowOptsType      `json:"winopts"`
	OwnerUserId string              `json:"owneruserid"`
	ShareMode   string              `json:"sharemode"`
	ShareOpts   WindowShareOptsType `json:"shareopts"`
	Lines       []*LineType         `json:"lines"`
	Cmds        []*CmdType          `json:"cmds"`
	Remotes     []*RemoteInstance   `json:"remotes"`

	// only for updates
	Remove bool `json:"remove,omitempty"`
}

type ScreenOptsType struct {
	TabColor string `json:"tabcolor"`
}

func (opts *ScreenOptsType) Scan(val interface{}) error {
	return quickScanJson(opts, val)
}

func (opts ScreenOptsType) Value() (driver.Value, error) {
	return quickValueJson(opts)
}

type ScreenType struct {
	SessionId      string              `json:"sessionid"`
	ScreenId       string              `json:"screenid"`
	ScreenIdx      int64               `json:"screenidx"`
	Name           string              `json:"name"`
	ActiveWindowId string              `json:"activewindowid"`
	ScreenOpts     ScreenOptsType      `json:"screenopts"`
	OwnerUserId    string              `json:"owneruserid"`
	ShareMode      string              `json:"sharemode"`
	Windows        []*ScreenWindowType `json:"windows"`

	// only for updates
	Remove bool `json:"remove,omitempty"`
	Full   bool `json:"full,omitempty"`
}

const (
	LayoutFull = "full"
)

type LayoutType struct {
	Type   string `json:"type"`
	Parent string `json:"parent,omitempty"`
	ZIndex int64  `json:"zindex,omitempty"`
	Float  bool   `json:"float,omitempty"`
	Top    string `json:"top,omitempty"`
	Bottom string `json:"bottom,omitempty"`
	Left   string `json:"left,omitempty"`
	Right  string `json:"right,omitempty"`
	Width  string `json:"width,omitempty"`
	Height string `json:"height,omitempty"`
}

func (l *LayoutType) Scan(val interface{}) error {
	return quickScanJson(l, val)
}

func (l LayoutType) Value() (driver.Value, error) {
	return quickValueJson(l)
}

type ScreenWindowType struct {
	SessionId string     `json:"sessionid"`
	ScreenId  string     `json:"screenid"`
	WindowId  string     `json:"windowid"`
	Name      string     `json:"name"`
	Layout    LayoutType `json:"layout"`

	// only for updates
	Remove bool `json:"remove,omitempty"`
}

type HistoryItemType struct {
	HistoryId string `json:"historyid"`
	Ts        int64  `json:"ts"`
	UserId    string `json:"userid"`
	SessionId string `json:"sessionid"`
	ScreenId  string `json:"screenid"`
	WindowId  string `json:"windowid"`
	LineId    string `json:"lineid"`
	HadError  bool   `json:"haderror"`
	CmdId     string `json:"cmdid"`
	CmdStr    string `json:"cmdstr"`

	// only for updates
	Remove bool `json:"remove"`
}

type RemoteState struct {
	Cwd string `json:"cwd"`
}

func (s *RemoteState) Scan(val interface{}) error {
	return quickScanJson(s, val)
}

func (s RemoteState) Value() (driver.Value, error) {
	return quickValueJson(s)
}

type TermOpts struct {
	Rows     int64 `json:"rows"`
	Cols     int64 `json:"cols"`
	FlexRows bool  `json:"flexrows,omitempty"`
	CmdSize  int64 `json:"cmdsize,omitempty"`
}

func (opts *TermOpts) Scan(val interface{}) error {
	return quickScanJson(opts, val)
}

func (opts TermOpts) Value() (driver.Value, error) {
	return quickValueJson(opts)
}

type RemoteInstance struct {
	RIId         string      `json:"riid"`
	Name         string      `json:"name"`
	SessionId    string      `json:"sessionid"`
	WindowId     string      `json:"windowid"`
	RemoteId     string      `json:"remoteid"`
	SessionScope bool        `json:"sessionscope"`
	State        RemoteState `json:"state"`

	// only for updates
	Remove bool `json:"remove,omitempty"`
}

type LineType struct {
	SessionId string `json:"sessionid"`
	WindowId  string `json:"windowid"`
	LineId    string `json:"lineid"`
	Ts        int64  `json:"ts"`
	UserId    string `json:"userid"`
	LineType  string `json:"linetype"`
	Text      string `json:"text,omitempty"`
	CmdId     string `json:"cmdid,omitempty"`
	Remove    bool   `json:"remove,omitempty"`
}

type SSHOpts struct {
	SSHHost     string `json:"sshhost"`
	SSHOptsStr  string `json:"sshopts"`
	SSHIdentity string `json:"sshidentity"`
	SSHUser     string `json:"sshuser"`
}

type RemoteType struct {
	RemoteId            string                 `json:"remoteid"`
	PhysicalId          string                 `json:"physicalid"`
	RemoteType          string                 `json:"remotetype"`
	RemoteAlias         string                 `json:"remotealias"`
	RemoteCanonicalName string                 `json:"remotecanonicalname"`
	RemoteSudo          bool                   `json:"remotesudo"`
	RemoteUser          string                 `json:"remoteuser"`
	RemoteHost          string                 `json:"remotehost"`
	AutoConnect         bool                   `json:"autoconnect"`
	InitPk              *packet.InitPacketType `json:"inipk"`
	SSHOpts             *SSHOpts               `json:"sshopts"`
	LastConnectTs       int64                  `json:"lastconnectts"`
}

func (r *RemoteType) GetName() string {
	if r.RemoteAlias != "" {
		return r.RemoteAlias
	}
	if r.RemoteUser == "" {
		return r.RemoteHost
	}
	return fmt.Sprintf("%s@%s", r.RemoteUser, r.RemoteHost)
}

type CmdType struct {
	SessionId   string                     `json:"sessionid"`
	CmdId       string                     `json:"cmdid"`
	RemoteId    string                     `json:"remoteid"`
	CmdStr      string                     `json:"cmdstr"`
	RemoteState RemoteState                `json:"remotestate"`
	TermOpts    TermOpts                   `json:"termopts"`
	Status      string                     `json:"status"`
	StartPk     *packet.CmdStartPacketType `json:"startpk"`
	DonePk      *packet.CmdDonePacketType  `json:"donepk"`
	UsedRows    int64                      `json:"usedrows"`
	RunOut      []packet.PacketType        `json:"runout"`
	Remove      bool                       `json:"remove"`
}

func (r *RemoteType) ToMap() map[string]interface{} {
	rtn := make(map[string]interface{})
	rtn["remoteid"] = r.RemoteId
	rtn["physicalid"] = r.PhysicalId
	rtn["remotetype"] = r.RemoteType
	rtn["remotealias"] = r.RemoteAlias
	rtn["remotecanonicalname"] = r.RemoteCanonicalName
	rtn["remotesudo"] = r.RemoteSudo
	rtn["remoteuser"] = r.RemoteUser
	rtn["remotehost"] = r.RemoteHost
	rtn["autoconnect"] = r.AutoConnect
	rtn["initpk"] = quickJson(r.InitPk)
	rtn["sshopts"] = quickJson(r.SSHOpts)
	rtn["lastconnectts"] = r.LastConnectTs
	return rtn
}

func RemoteFromMap(m map[string]interface{}) *RemoteType {
	if len(m) == 0 {
		return nil
	}
	var r RemoteType
	quickSetStr(&r.RemoteId, m, "remoteid")
	quickSetStr(&r.PhysicalId, m, "physicalid")
	quickSetStr(&r.RemoteType, m, "remotetype")
	quickSetStr(&r.RemoteAlias, m, "remotealias")
	quickSetStr(&r.RemoteCanonicalName, m, "remotecanonicalname")
	quickSetBool(&r.RemoteSudo, m, "remotesudo")
	quickSetStr(&r.RemoteUser, m, "remoteuser")
	quickSetStr(&r.RemoteHost, m, "remotehost")
	quickSetBool(&r.AutoConnect, m, "autoconnect")
	quickSetJson(&r.InitPk, m, "initpk")
	quickSetJson(&r.SSHOpts, m, "sshopts")
	quickSetInt64(&r.LastConnectTs, m, "lastconnectts")
	return &r
}

func (cmd *CmdType) ToMap() map[string]interface{} {
	rtn := make(map[string]interface{})
	rtn["sessionid"] = cmd.SessionId
	rtn["cmdid"] = cmd.CmdId
	rtn["remoteid"] = cmd.RemoteId
	rtn["cmdstr"] = cmd.CmdStr
	rtn["remotestate"] = quickJson(cmd.RemoteState)
	rtn["termopts"] = quickJson(cmd.TermOpts)
	rtn["status"] = cmd.Status
	rtn["startpk"] = quickJson(cmd.StartPk)
	rtn["donepk"] = quickJson(cmd.DonePk)
	rtn["runout"] = quickJson(cmd.RunOut)
	rtn["usedrows"] = cmd.UsedRows
	return rtn
}

func CmdFromMap(m map[string]interface{}) *CmdType {
	if len(m) == 0 {
		return nil
	}
	var cmd CmdType
	quickSetStr(&cmd.SessionId, m, "sessionid")
	quickSetStr(&cmd.CmdId, m, "cmdid")
	quickSetStr(&cmd.RemoteId, m, "remoteid")
	quickSetStr(&cmd.CmdStr, m, "cmdstr")
	quickSetJson(&cmd.RemoteState, m, "remotestate")
	quickSetJson(&cmd.TermOpts, m, "termopts")
	quickSetStr(&cmd.Status, m, "status")
	quickSetJson(&cmd.StartPk, m, "startpk")
	quickSetJson(&cmd.DonePk, m, "donepk")
	quickSetJson(&cmd.RunOut, m, "runout")
	quickSetInt64(&cmd.UsedRows, m, "usedrows")
	return &cmd
}

func makeNewLineCmd(sessionId string, windowId string, userId string, cmdId string) *LineType {
	rtn := &LineType{}
	rtn.SessionId = sessionId
	rtn.WindowId = windowId
	rtn.LineId = uuid.New().String()
	rtn.Ts = time.Now().UnixMilli()
	rtn.UserId = userId
	rtn.LineType = LineTypeCmd
	rtn.CmdId = cmdId
	return rtn
}

func makeNewLineText(sessionId string, windowId string, userId string, text string) *LineType {
	rtn := &LineType{}
	rtn.SessionId = sessionId
	rtn.WindowId = windowId
	rtn.LineId = uuid.New().String()
	rtn.Ts = time.Now().UnixMilli()
	rtn.UserId = userId
	rtn.LineType = LineTypeText
	rtn.Text = text
	return rtn
}

func AddCommentLine(ctx context.Context, sessionId string, windowId string, userId string, commentText string) (*LineType, error) {
	rtnLine := makeNewLineText(sessionId, windowId, userId, commentText)
	err := InsertLine(ctx, rtnLine, nil)
	if err != nil {
		return nil, err
	}
	return rtnLine, nil
}

func AddCmdLine(ctx context.Context, sessionId string, windowId string, userId string, cmd *CmdType) (*LineType, error) {
	rtnLine := makeNewLineCmd(sessionId, windowId, userId, cmd.CmdId)
	err := InsertLine(ctx, rtnLine, cmd)
	if err != nil {
		return nil, err
	}
	return rtnLine, nil
}

func EnsureLocalRemote(ctx context.Context) error {
	remoteId, err := base.GetRemoteId()
	if err != nil {
		return fmt.Errorf("getting local remoteid: %w", err)
	}
	remote, err := GetRemoteById(ctx, remoteId)
	if err != nil {
		return fmt.Errorf("getting remote[%s] from db: %w", remoteId, err)
	}
	if remote != nil {
		return nil
	}
	hostName, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("getting hostname: %w", err)
	}
	user, err := user.Current()
	if err != nil {
		return fmt.Errorf("getting user: %w", err)
	}
	// create the local remote
	localRemote := &RemoteType{
		RemoteId:            remoteId,
		RemoteType:          "ssh",
		RemoteAlias:         LocalRemoteName,
		RemoteCanonicalName: fmt.Sprintf("%s@%s", user.Username, hostName),
		RemoteSudo:          false,
		RemoteUser:          user.Username,
		RemoteHost:          hostName,
		AutoConnect:         true,
	}
	err = InsertRemote(ctx, localRemote)
	if err != nil {
		return err
	}
	log.Printf("[db] added remote '%s', id=%s\n", localRemote.GetName(), localRemote.RemoteId)
	return nil
}

func EnsureDefaultSession(ctx context.Context) (*SessionType, error) {
	session, err := GetSessionByName(ctx, DefaultSessionName)
	if err != nil {
		return nil, err
	}
	if session != nil {
		return session, nil
	}
	_, err = InsertSessionWithName(ctx, DefaultSessionName, true)
	if err != nil {
		return nil, err
	}
	return GetSessionByName(ctx, DefaultSessionName)
}

func createUserData(tx *TxWrap) error {
	userId := uuid.New().String()
	curve := elliptic.P384()
	pkey, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		return fmt.Errorf("generating P-834 key: %w", err)
	}
	pkBytes, err := x509.MarshalECPrivateKey(pkey)
	if err != nil {
		return fmt.Errorf("marshaling (pkcs8) private key bytes: %w", err)
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&pkey.PublicKey)
	if err != nil {
		return fmt.Errorf("marshaling (pkix) public key bytes: %w", err)
	}
	query := `INSERT INTO client (userid, userpublickeybytes, userprivatekeybytes) VALUES (?, ?, ?)`
	tx.ExecWrap(query, userId, pubBytes, pkBytes)
	fmt.Printf("create new userid[%s] with public/private keypair\n", userId)
	return nil
}

func EnsureUserData(ctx context.Context) (*UserData, error) {
	var rtn UserData
	err := WithTx(ctx, func(tx *TxWrap) error {
		query := `SELECT count(*) FROM client`
		count := tx.GetInt(query)
		if count > 1 {
			return fmt.Errorf("invalid client database, multiple (%d) rows in client table", count)
		}
		if count == 0 {
			createErr := createUserData(tx)
			if createErr != nil {
				return createErr
			}
		}
		found := tx.GetWrap(&rtn, "SELECT * FROM client")
		if !found {
			return fmt.Errorf("invalid client data")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if rtn.UserId == "" {
		return nil, fmt.Errorf("invalid client data (no userid)")
	}
	if len(rtn.UserPrivateKeyBytes) == 0 || len(rtn.UserPublicKeyBytes) == 0 {
		return nil, fmt.Errorf("invalid client data (no public/private keypair)")
	}
	rtn.UserPrivateKey, err = x509.ParseECPrivateKey(rtn.UserPrivateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid client data, cannot parse private key: %w", err)
	}
	pubKey, err := x509.ParsePKIXPublicKey(rtn.UserPublicKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid client data, cannot parse public key: %w", err)
	}
	var ok bool
	rtn.UserPublicKey, ok = pubKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("invalid client data, wrong public key type: %T", pubKey)
	}
	return &rtn, nil
}
