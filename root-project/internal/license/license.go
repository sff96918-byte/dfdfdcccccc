package license

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yusufpapurcu/wmi"
)

type CheckRequest struct {
	Key string `json:"key"`
}

type CheckResponse struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
	Expiry string `json:"expiry,omitempty"`
}

// GenerateHardwareKey generates a unique MD5 hash based on hardware serials.
func GenerateHardwareKey() (string, error) {
	var csp []struct {
		UUID              string
		IdentifyingNumber string
	}
	var bios []struct {
		SerialNumber string
	}
	var disk []struct {
		SerialNumber string
	}

	var raw string

	if err := wmi.Query("SELECT UUID, IdentifyingNumber FROM Win32_ComputerSystemProduct", &csp); err == nil && len(csp) > 0 {
		raw += csp[0].UUID + csp[0].IdentifyingNumber
	}
	if err := wmi.Query("SELECT SerialNumber FROM Win32_BIOS", &bios); err == nil && len(bios) > 0 {
		raw += bios[0].SerialNumber
	}
	if err := wmi.Query("SELECT SerialNumber FROM Win32_DiskDrive", &disk); err == nil && len(disk) > 0 {
		raw += disk[0].SerialNumber
	}

	if raw == "" {
		return "", fmt.Errorf("failed to retrieve hardware identifiers")
	}

	hash := md5.Sum([]byte(raw))
	return strings.ToUpper(hex.EncodeToString(hash[:])), nil
}

// VerifyLicense checks if the provided key is valid by querying the remote API.
func VerifyLicense(key string) (bool, string) {
	reqBody := CheckRequest{Key: key}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return false, "failed to build request"
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post("https://license-api.nilhasancl.workers.dev/check", "application/json", bytes.NewReader(b))
	if err != nil {
		return false, "network error: " + err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("server returned status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "failed to read response"
	}

	var res CheckResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return false, "failed to parse response"
	}

	if res.Valid {
		return true, ""
	}

	if res.Reason != "" {
		return false, res.Reason
	}

	return false, "invalid or not found"
}
