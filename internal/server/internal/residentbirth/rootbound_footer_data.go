package residentbirth

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"getarm.org/internal/server/internal/residentservice"
)

const MaxP1RootBoundFooterBytes = 4 << 10
const MaxP1LaunchJournalBytes = 20 << 10
const p1FooterHeaderBytes = 44

var p1FooterMagic = [8]byte{'A', 'R', 'M', 'R', 'B', 'F', '1', '\n'}
var p1FooterEnd = [8]byte{'E', 'N', 'D', 'R', 'B', 'F', '1', '\n'}

// Closed canonical DATA. The variable original key is committed with its own
// domain; original intent/prepared/service records retain the exact key link.
// No home path, settings object, secret, success bit or authority constructor.
type P1RootBoundFooterData struct {
	Schema                   uint32 `json:"schema"`
	Domain                   string `json:"domain"`
	Generation               string `json:"generation"`
	DebitID                  string `json:"debit_id"`
	LaunchKeySHA256          string `json:"launch_key_sha256"`
	PreparedCommitmentSHA256 string `json:"prepared_commitment_sha256"`
	IntentSHA256             string `json:"intent_sha256"`
	ProfileSHA256            string `json:"profile_sha256"`
	ImageSHA256              string `json:"image_sha256"`
	SettingsSHA256           string `json:"settings_sha256"`
	ServiceJournalBytes      int64  `json:"service_journal_bytes"`
	ServiceJournalSHA256     string `json:"service_journal_sha256"`
}

func footerDigestData(value string, bytes int) bool {
	if len(value) != bytes*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func P1LaunchKeyCommitmentData(key string) (string, error) {
	if len(key) == 0 || len(key) > 4096 || !utf8.ValidString(key) || strings.ContainsAny(key, "\x00\r\n") {
		return "", errOriginalFence
	}
	h := sha256.Sum256([]byte("arm-resident-launch-key-v1\x00" + key))
	return hex.EncodeToString(h[:]), nil
}
func P1FooterForServiceData(service residentservice.P1RootBoundData, intentSHA, settingsSHA string) (P1RootBoundFooterData, error) {
	key, err := P1LaunchKeyCommitmentData(service.Record.LaunchKey)
	if err != nil {
		return P1RootBoundFooterData{}, err
	}
	return P1RootBoundFooterData{Schema: 1, Domain: "arm-resident-root-bound-footer-v1", Generation: service.Record.Generation, DebitID: service.Record.DebitID, LaunchKeySHA256: key, PreparedCommitmentSHA256: service.Record.CommitmentSHA256, IntentSHA256: intentSHA, ProfileSHA256: service.ProfileSHA256, ImageSHA256: service.ImageSHA256, SettingsSHA256: settingsSHA, ServiceJournalBytes: service.JournalBytes, ServiceJournalSHA256: service.JournalSHA256}, nil
}
func EncodeP1RootBoundFooterData(data P1RootBoundFooterData) ([]byte, error) {
	if data.Schema != 1 || data.Domain != "arm-resident-root-bound-footer-v1" || !footerDigestData(data.Generation, 16) || data.ServiceJournalBytes <= 0 || data.ServiceJournalBytes > residentservice.MaxJournalBytes {
		return nil, errOriginalFence
	}
	for _, h := range []string{data.DebitID, data.LaunchKeySHA256, data.PreparedCommitmentSHA256, data.IntentSHA256, data.ProfileSHA256, data.ImageSHA256, data.SettingsSHA256, data.ServiceJournalSHA256} {
		if !footerDigestData(h, sha256.Size) {
			return nil, errOriginalFence
		}
	}
	payload, err := json.Marshal(data)
	if err != nil || len(payload) == 0 || p1FooterHeaderBytes+len(payload)+len(p1FooterEnd) > MaxP1RootBoundFooterBytes {
		return nil, errOriginalFence
	}
	frame := make([]byte, p1FooterHeaderBytes+len(payload)+len(p1FooterEnd))
	copy(frame[:8], p1FooterMagic[:])
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(payload)))
	h := sha256.Sum256(payload)
	copy(frame[12:44], h[:])
	copy(frame[44:], payload)
	copy(frame[44+len(payload):], p1FooterEnd[:])
	return frame, nil
}
func DecodeP1RootBoundFooterData(frame []byte) (P1RootBoundFooterData, error) {
	var data P1RootBoundFooterData
	if len(frame) <= p1FooterHeaderBytes+len(p1FooterEnd) || len(frame) > MaxP1RootBoundFooterBytes || !bytes.Equal(frame[:8], p1FooterMagic[:]) {
		return data, errOriginalFence
	}
	encodedLength := binary.BigEndian.Uint32(frame[8:12])
	if uint64(encodedLength) != uint64(len(frame)-p1FooterHeaderBytes-len(p1FooterEnd)) || !bytes.Equal(frame[len(frame)-len(p1FooterEnd):], p1FooterEnd[:]) {
		return data, errOriginalFence
	}
	length := int(encodedLength) // equality above bounds this conversion by the <=4KiB frame
	payload := frame[p1FooterHeaderBytes : p1FooterHeaderBytes+length]
	h := sha256.Sum256(payload)
	if !bytes.Equal(h[:], frame[12:44]) {
		return data, errOriginalFence
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if d.Decode(&data) != nil || d.Decode(new(any)) != io.EOF {
		return P1RootBoundFooterData{}, errOriginalFence
	}
	canonical, err := EncodeP1RootBoundFooterData(data)
	if err != nil || !bytes.Equal(frame, canonical) {
		return P1RootBoundFooterData{}, errOriginalFence
	}
	return data, nil
}
func P1LaunchJournalCapacityData(intentBytes, footerBytes int) (int, error) {
	if intentBytes <= 0 || intentBytes > maxIntentBytes || footerBytes <= 0 || footerBytes > MaxP1RootBoundFooterBytes || footerBytes > MaxP1LaunchJournalBytes-intentBytes {
		return 0, errOriginalFence
	}
	return intentBytes + footerBytes, nil
}
