package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	v2service "github.com/AAAYNMMM/CWapi/internal/v2/service"
	"github.com/wailsapp/wails/v2/pkg/options"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func TestSingleInstanceIDsRemainStable(t *testing.T) {
	if cwapiSingleInstanceID != "007f7623-c85d-481d-be63-7e6887667f4c" {
		t.Fatalf("normal single-instance ID changed: %q", cwapiSingleInstanceID)
	}
	if cwapiProbeSingleInstanceID != "90e9bd2a-3834-4ba4-95e8-9f4683543610" {
		t.Fatalf("probe single-instance ID changed: %q", cwapiProbeSingleInstanceID)
	}

	t.Setenv("CWAPI_GUI_PROBE_CONFIG", "")
	if got := currentSingleInstanceID(); got != cwapiSingleInstanceID {
		t.Fatalf("normal current ID=%q want %q", got, cwapiSingleInstanceID)
	}
	t.Setenv("CWAPI_GUI_PROBE_CONFIG", "probe.json")
	if got := currentSingleInstanceID(); got != cwapiProbeSingleInstanceID {
		t.Fatalf("probe current ID=%q want %q", got, cwapiProbeSingleInstanceID)
	}
}

func TestApplicationOptionsKeepWailsSingleInstanceLock(t *testing.T) {
	t.Setenv("CWAPI_GUI_PROBE_CONFIG", "")
	app := NewApp()
	opts := applicationOptions(app)
	if opts.SingleInstanceLock == nil {
		t.Fatal("SingleInstanceLock must remain configured")
	}
	if opts.SingleInstanceLock.UniqueId != cwapiSingleInstanceID {
		t.Fatalf("UniqueId=%q want %q", opts.SingleInstanceLock.UniqueId, cwapiSingleInstanceID)
	}
	if opts.SingleInstanceLock.OnSecondInstanceLaunch == nil {
		t.Fatal("OnSecondInstanceLaunch must remain configured")
	}
}

func TestSecondInstanceLaunchRestoresWindowAndWarnsWithoutServiceMutation(t *testing.T) {
	oldUnminimise := secondInstanceWindowUnminimise
	oldShow := secondInstanceWindowShow
	oldDialog := secondInstanceMessageDialog
	t.Cleanup(func() {
		secondInstanceWindowUnminimise = oldUnminimise
		secondInstanceWindowShow = oldShow
		secondInstanceMessageDialog = oldDialog
	})

	ctx := context.WithValue(context.Background(), struct{}{}, "runtime")
	service := &v2service.Service{}
	startupErr := errors.New("sentinel startup error")
	app := NewApp()
	app.ctx = ctx
	app.configPath = "sentinel-config"
	app.service = service
	app.startupErr = startupErr

	var calls []string
	secondInstanceWindowUnminimise = func(got context.Context) {
		if got != ctx {
			t.Fatal("unminimise received wrong context")
		}
		calls = append(calls, "unminimise")
	}
	secondInstanceWindowShow = func(got context.Context) {
		if got != ctx {
			t.Fatal("show received wrong context")
		}
		calls = append(calls, "show")
	}
	secondInstanceMessageDialog = func(got context.Context, dialog wailsruntime.MessageDialogOptions) (string, error) {
		if got != ctx {
			t.Fatal("dialog received wrong context")
		}
		calls = append(calls, "dialog")
		if dialog.Type != wailsruntime.WarningDialog {
			t.Fatalf("dialog type=%q want warning", dialog.Type)
		}
		if dialog.Title != secondInstanceDialogTitle {
			t.Fatalf("dialog title=%q", dialog.Title)
		}
		if dialog.Message != secondInstanceDialogMessage {
			t.Fatalf("dialog message=%q", dialog.Message)
		}
		return "Ok", nil
	}

	app.onSecondInstanceLaunch(options.SecondInstanceData{})

	if !reflect.DeepEqual(calls, []string{"unminimise", "show", "dialog"}) {
		t.Fatalf("callback calls=%v", calls)
	}
	if app.service != service {
		t.Fatal("second-instance callback must not replace/restart service")
	}
	if app.configPath != "sentinel-config" || app.startupErr != startupErr {
		t.Fatal("second-instance callback must not mutate service configuration/startup state")
	}
}
