package control

import (
	"fmt"
	"testing"

	"github.com/aws/smithy-go"
)

func TestFailureMessageCarriesWhatAwsRefused(t *testing.T) {
	refused := &smithy.GenericAPIError{
		Code: "InsufficientInstanceCapacity",
		Message: "We currently do not have sufficient t3a.medium capacity in the Availability Zone " +
			"you requested (eu-central-1c).",
	}
	err := fmt.Errorf("launching a server for vs-test2: operation error EC2: RunInstances, "+
		"exceeded maximum number of attempts, 3: %w", refused)

	got := failureMessage("createServer", err)
	want := "Couldn't create the server: InsufficientInstanceCapacity"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestFailureMessageCarriesAnErrorOfOurOwn(t *testing.T) {
	got := failureMessage("deleteVolume", fmt.Errorf("reading volume vol-1: %w", fmt.Errorf("no such volume")))
	want := "Couldn't delete that volume: reading volume vol-1: no such volume"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestFailureMessageNamesACommandItHasNoWordingFor(t *testing.T) {
	got := failureMessage("somethingNew", fmt.Errorf("the disk is on fire"))
	want := "Something went wrong: the disk is on fire"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestTheReplySaysWhetherTheCommandHappened(t *testing.T) {
	if happened := done("Creating vs-test3", Info{})[2]; happened != true {
		t.Errorf("a command that ran reported %v", happened)
	}
	if happened := refused("InsufficientInstanceCapacity", Info{})[2]; happened != false {
		t.Errorf("a command that was refused reported %v", happened)
	}
}
