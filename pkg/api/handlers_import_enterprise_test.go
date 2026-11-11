package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
	"github.com/jasonczech/apexload/pkg/storage"
)

const testSalesforceVuGenActionC = `Action()
{
	web_custom_request("AuraAction",
		"URL=https://example.lightning.force.com/aura?r=1",
		"Method=POST",
		"Resource=0",
		"EncType=application/x-www-form-urlencoded; charset=UTF-8",
		"Body=message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%3Ba%22%2C%22descriptor%22%3A%22aura%3A%2F%2FRecordUiController%2FACTION%24getRecord%22%2C%22callingDescriptor%22%3A%22UNKNOWN%22%2C%22params%22%3A%7B%22recordId%22%3A%22001000000000001AAA%22%7D%7D%5D%7D&aura.context=%7B%22mode%22%3A%22PROD%22%2C%22fwuid%22%3A%22fwuid-1%22%7D&aura.pageURI=%2Flightning%2Fr%2FAccount%2F001000000000001AAA%2Fview&aura.token=token-1",
		LAST);
	return 0;
}
`

const testSalesforceBundledReplayHAR = `{
  "log": {
    "version": "1.2",
    "entries": [
      {
        "request": {
          "method": "POST",
          "url": "https://example.lightning.force.com/aura?r=1",
          "postData": {
            "mimeType": "application/x-www-form-urlencoded",
            "text": "message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%3Ba%22%2C%22descriptor%22%3A%22aura%3A%2F%2FRecordUiController%2FACTION%24getRecord%22%2C%22callingDescriptor%22%3A%22UNKNOWN%22%2C%22params%22%3A%7B%22recordId%22%3A%22001000000000001AAA%22%7D%7D%5D%7D&aura.context=%7B%22mode%22%3A%22PROD%22%2C%22fwuid%22%3A%22fwuid-1%22%7D&aura.pageURI=%2Flightning%2Fr%2FAccount%2F001000000000001AAA%2Fview&aura.token=token-1"
          }
        },
        "response": {
          "status": 200,
          "content": {
            "mimeType": "application/json",
            "text": "{\"actions\":[{\"id\":\"1;a\",\"state\":\"ERROR\",\"error\":[{\"message\":\"Record inaccessible\"}]}]}"
          }
        }
      }
    ]
  }
}`

func extractSalesforceReviewFromResponseMap(t *testing.T, respMap map[string]any) salesforce.Review {
	t.Helper()
	rawScript, ok := respMap["script"].(map[string]any)
	if !ok {
		t.Fatalf("missing script in response: %#v", respMap)
	}
	metadata, _ := rawScript["metadata"].(map[string]any)
	importMeta, _ := metadata["import"].(map[string]any)
	preserved, _ := importMeta["preserved"].(map[string]any)
	rawReview, ok := preserved["salesforce_review"]
	if !ok || rawReview == nil {
		t.Fatalf("missing metadata.import.preserved.salesforce_review in script: %#v", rawScript)
	}
	encoded, err := json.Marshal(rawReview)
	if err != nil {
		t.Fatal(err)
	}
	var review salesforce.Review
	if err := json.Unmarshal(encoded, &review); err != nil {
		t.Fatal(err)
	}
	return review
}

func TestEnterpriseArchiveImportAutoDetectsBundledReplayHAR(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-har.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, ImportAssetDir: t.TempDir()})

	zipData := enterpriseCollectionTestZIP(t, map[string][]byte{
		"SalesforceScript/SalesforceScript.usr": []byte("[General]\nType=Web\nRuntimeSettings=default.cfg\n\n[Actions]\nAction=Action.c\n"),
		"SalesforceScript/default.cfg":          []byte("[HTTP]\nConnectTimeout=20\n"),
		"SalesforceScript/Action.c":             []byte(testSalesforceVuGenActionC),
		"SalesforceScript/replay.har":           []byte(testSalesforceBundledReplayHAR),
		"SalesforceScript/result1/replay.har":   []byte("not valid json in result1"),
	})

	for _, route := range []string{"/api/v1/scripts/import/enterprise-script-archive", "/api/v1/scripts/import/enterprise-script-archive/validate"} {
		req := enterpriseCollectionTestRequest(t, route, zipData, nil)
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", route, rec.Code, rec.Body.String())
		}
		var respMap map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &respMap); err != nil {
			t.Fatal(err)
		}
		review := extractSalesforceReviewFromResponseMap(t, respMap)
		if !review.Detected || review.AuraOutcomes == nil || review.AuraOutcomes.StateCounts[aura.Error] != 1 {
			t.Fatalf("%s expected HAR-correlated AuraOutcomes with 1 ERROR, got %#v", route, review.AuraOutcomes)
		}
	}
}

func TestEnterpriseArchiveImportIgnoresCorruptBundledReplayHARWithoutWarning(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-corrupt-har.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, ImportAssetDir: t.TempDir()})

	zipData := enterpriseCollectionTestZIP(t, map[string][]byte{
		"SalesforceScript/SalesforceScript.usr": []byte("[General]\nType=Web\nRuntimeSettings=default.cfg\n\n[Actions]\nAction=Action.c\n"),
		"SalesforceScript/default.cfg":          []byte("[HTTP]\nConnectTimeout=20\n"),
		"SalesforceScript/Action.c":             []byte(testSalesforceVuGenActionC),
		"SalesforceScript/replay.har":           []byte("{corrupt-har"),
	})

	req := enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive/validate", zipData, nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Salesforce review failed") {
		t.Fatalf("corrupt bundled replay.har should be ignored without warning, got: %s", rec.Body.String())
	}
}

func TestEnterpriseArchiveFolderUploadAutoDetectsBundledReplayHAR(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-folder-har.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, ImportAssetDir: t.TempDir()})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("project_id", storage.DefaultProjectID)
	files := map[string]string{
		"file:SalesforceScript/SalesforceScript.usr": "[General]\nType=Web\nRuntimeSettings=default.cfg\n\n[Actions]\nAction=Action.c\n",
		"file:SalesforceScript/default.cfg":          "[HTTP]\nConnectTimeout=20\n",
		"file:SalesforceScript/Action.c":             testSalesforceVuGenActionC,
		"file:SalesforceScript/replay.har":           testSalesforceBundledReplayHAR,
	}
	for field, content := range files {
		part, createErr := writer.CreateFormFile(field, filepath.Base(field))
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := part.Write([]byte(content)); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive/validate", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var respMap map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &respMap); err != nil {
		t.Fatal(err)
	}
	review := extractSalesforceReviewFromResponseMap(t, respMap)
	if !review.Detected || review.AuraOutcomes == nil || review.AuraOutcomes.StateCounts[aura.Error] != 1 {
		t.Fatalf("folder upload expected HAR-correlated AuraOutcomes with 1 ERROR, got %#v", review.AuraOutcomes)
	}
}

func TestEnterpriseArchiveCollectionAutoDetectsMemberBundledReplayHAR(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-collection-har.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, ImportAssetDir: t.TempDir()})

	memberZip := enterpriseCollectionTestZIP(t, map[string][]byte{
		"SalesforceScript/SalesforceScript.usr": []byte("[General]\nType=Web\nRuntimeSettings=default.cfg\n\n[Actions]\nAction=Action.c\n"),
		"SalesforceScript/default.cfg":          []byte("[HTTP]\nConnectTimeout=20\n"),
		"SalesforceScript/Action.c":             []byte(testSalesforceVuGenActionC),
		"SalesforceScript/replay.har":           []byte(testSalesforceBundledReplayHAR),
	})
	outerZip := enterpriseCollectionTestZIP(t, map[string][]byte{
		"package/SalesforceMember.usz": memberZip,
	})

	req := enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive/validate", outerZip, nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("collection status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var collectionResp struct {
		ArchiveCollection bool                                `json:"archive_collection"`
		Archives          []enterpriseArchiveCollectionMember `json:"archives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &collectionResp); err != nil {
		t.Fatal(err)
	}
	if !collectionResp.ArchiveCollection || len(collectionResp.Archives) != 1 || collectionResp.Archives[0].Response == nil {
		t.Fatalf("unexpected collection response: %#v", collectionResp)
	}
	review := extractSalesforceReviewFromResponseMap(t, collectionResp.Archives[0].Response)
	if !review.Detected || review.AuraOutcomes == nil || review.AuraOutcomes.StateCounts[aura.Error] != 1 {
		t.Fatalf("collection member expected HAR-correlated AuraOutcomes with 1 ERROR, got %#v", review.AuraOutcomes)
	}
}

func TestWriteErrorPreservesStepCoordinatesAndMultiUnderscoreActionNames(t *testing.T) {
	t.Run("preserves long composite VuGen and Aura step coordinate action names", func(t *testing.T) {
		msg := `invalid script: actions[1] "Start_iPadTest_LrgeBANs_ViewServProm_T08_ClkBackToAccInfo||aura_823_0_s_2_1754691916807_1_ACTION$getRecord_transaction": assertions[0]: value is required`
		rec := httptest.NewRecorder()
		writeError(rec, http.StatusBadRequest, msg)
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["error"] != msg {
			t.Fatalf("got %q, want %q", body["error"], msg)
		}
	})

	t.Run("preserves nested control-flow and sequence request coordinates", func(t *testing.T) {
		msg := `init[0].control.cases[1].steps[2].params.requests[0] "Checkout_Step_T09_VerifyOrder_Confirmation_Action": missing target`
		rec := httptest.NewRecorder()
		writeError(rec, http.StatusUnprocessableEntity, msg)
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["error"] != msg {
			t.Fatalf("got %q, want %q", body["error"], msg)
		}
	})

	t.Run("redacts real secrets inside and outside step coordinate action labels", func(t *testing.T) {
		jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
		stripeKey := "sk_live_51H8abc123def456ghi789jkl012"
		msg := `actions[0] "Start_iPadTest_LrgeBANs_ViewServProm_T08_ClkBackToAccInfo ?password=my_secret_pw_123 ` + jwt + ` ` + stripeKey + `": header token dGhpcyBpcyBhIHNlY3JldCB0b2tlbiB2YWx1ZQ== rejected`
		rec := httptest.NewRecorder()
		writeError(rec, http.StatusBadRequest, msg)
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		got := body["error"]
		if !strings.Contains(got, `actions[0] "Start_iPadTest_LrgeBANs_ViewServProm_T08_ClkBackToAccInfo`) {
			t.Fatalf("expected action identifier preserved, got %q", got)
		}
		for _, secret := range []string{"my_secret_pw_123", jwt, stripeKey, "dGhpcyBpcyBhIHNlY3JldCB0b2tlbiB2YWx1ZQ=="} {
			if strings.Contains(got, secret) {
				t.Fatalf("expected secret %q to be redacted in %q", secret, got)
			}
		}
	})
}
