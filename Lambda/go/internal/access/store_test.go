package access

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeTable is the grants table in memory.
type fakeTable struct {
	items map[string]map[string]ddbtypes.AttributeValue
}

func newFakeTable(rows ...Grant) *fakeTable {
	f := &fakeTable{items: map[string]map[string]ddbtypes.AttributeValue{}}
	for _, r := range rows {
		f.put(map[string]ddbtypes.AttributeValue{
			keyUserSub: &ddbtypes.AttributeValueMemberS{Value: r.UserSub},
			keyServer:  &ddbtypes.AttributeValueMemberS{Value: r.ServerID},
			"role":     &ddbtypes.AttributeValueMemberS{Value: r.Role},
		})
	}
	return f
}

func (f *fakeTable) put(item map[string]ddbtypes.AttributeValue) {
	f.items[stringAttr(item, keyUserSub)+"|"+stringAttr(item, keyServer)] = item
}

func (f *fakeTable) rows() []Grant {
	out := []Grant{}
	for _, item := range f.items {
		out = append(out, Grant{
			UserSub:  stringAttr(item, keyUserSub),
			ServerID: stringAttr(item, keyServer),
			Role:     stringAttr(item, "role"),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UserSub != out[j].UserSub {
			return out[i].UserSub < out[j].UserSub
		}
		return out[i].ServerID < out[j].ServerID
	})
	return out
}

func (f *fakeTable) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	wanted := stringValue(in.ExpressionAttributeValues[":sub"])
	out := &dynamodb.QueryOutput{}
	for _, item := range f.items {
		if stringAttr(item, keyUserSub) == wanted {
			out.Items = append(out.Items, item)
		}
	}
	return out, nil
}

func (f *fakeTable) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	filtered := ""
	if in.FilterExpression != nil {
		if !strings.Contains(*in.FilterExpression, ":server") {
			return nil, fmt.Errorf("fakeTable can't apply the filter %q", *in.FilterExpression)
		}
		filtered = stringValue(in.ExpressionAttributeValues[":server"])
	}
	out := &dynamodb.ScanOutput{}
	for _, item := range f.items {
		if filtered == "" || stringAttr(item, keyServer) == filtered {
			out.Items = append(out.Items, item)
		}
	}
	return out, nil
}

func (f *fakeTable) BatchWriteItem(_ context.Context, in *dynamodb.BatchWriteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	for _, writes := range in.RequestItems {
		for _, w := range writes {
			switch {
			case w.PutRequest != nil:
				f.put(w.PutRequest.Item)
			case w.DeleteRequest != nil:
				delete(f.items, stringAttr(w.DeleteRequest.Key, keyUserSub)+"|"+
					stringAttr(w.DeleteRequest.Key, keyServer))
			}
		}
	}
	return &dynamodb.BatchWriteItemOutput{}, nil
}

func (f *fakeTable) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.put(in.Item)
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeTable) UpdateItem(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return nil, fmt.Errorf("no test here updates an item")
}

func stringValue(v ddbtypes.AttributeValue) string {
	if s, ok := v.(*ddbtypes.AttributeValueMemberS); ok {
		return s.Value
	}
	return ""
}

func TestForgetServerLeavesEveryOtherGrantAlone(t *testing.T) {
	table := newFakeTable(
		Grant{UserSub: "alice", ServerID: "vs-test", Role: "owner"},
		Grant{UserSub: "alice", ServerID: "valheim", Role: "player"},
		Grant{UserSub: "bob", ServerID: "vs-test", Role: "player"},
		Grant{UserSub: "bob", ServerID: permitKey, Role: ""},
		Grant{UserSub: defaultsSub, ServerID: defaultsSub, Role: "player"},
	)
	store := NewStore(table, "grants")

	if err := store.ForgetServer(context.Background(), "vs-test"); err != nil {
		t.Fatal(err)
	}

	want := []Grant{
		{UserSub: defaultsSub, ServerID: defaultsSub, Role: "player"},
		{UserSub: "alice", ServerID: "valheim", Role: "player"},
		{UserSub: "bob", ServerID: permitKey, Role: ""},
	}
	if got := table.rows(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("after forgetting vs-test\n got %v\nwant %v", got, want)
	}
}

func TestForgetServerStopsAReusedIdInheritingAccess(t *testing.T) {
	table := newFakeTable(
		Grant{UserSub: "alice", ServerID: "vs-test", Role: "owner"},
		Grant{UserSub: defaultsSub, ServerID: defaultsSub, Role: ""},
	)
	store := NewStore(table, "grants")
	ctx := context.Background()

	if err := store.ForgetServer(ctx, "vs-test"); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyDefaultsTo(ctx, "vs-test"); err != nil {
		t.Fatal(err)
	}

	held, err := store.RolesFor(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if role := held.Roles["vs-test"]; role != "" {
		t.Errorf("a server recreated under the same id granted %q to the last one's owner", role)
	}
	if For(false, held.Roles["vs-test"]).Has(View) {
		t.Error("the last owner can still see the new server")
	}
}

func TestApplyDefaultsToGrantsOnlyToPeopleWithAccessEverywhere(t *testing.T) {
	table := newFakeTable(
		Grant{UserSub: "everywhere", ServerID: "valheim", Role: "player"},
		Grant{UserSub: "everywhere", ServerID: "vintagestory", Role: "owner"},
		Grant{UserSub: "oneServerShort", ServerID: "valheim", Role: "player"},
		Grant{UserSub: "oneServerShort", ServerID: "vintagestory", Role: ""},
		Grant{UserSub: "revoked", ServerID: "valheim", Role: ""},
		Grant{UserSub: "revoked", ServerID: "vintagestory", Role: ""},
		Grant{UserSub: "permitOnly", ServerID: permitKey, Role: ""},
		Grant{UserSub: defaultsSub, ServerID: defaultsSub, Role: "player"},
	)
	store := NewStore(table, "grants")

	if err := store.ApplyDefaultsTo(context.Background(), "newserver"); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, g := range table.rows() {
		if g.ServerID == "newserver" {
			got[g.UserSub] = g.Role
		}
	}
	want := map[string]string{"everywhere": "player"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("grants on the new server\n got %v\nwant %v", got, want)
	}
}

func TestApplyDefaultsToNeverGrantsToTheDefaultsRow(t *testing.T) {
	table := newFakeTable(
		Grant{UserSub: "alice", ServerID: "valheim", Role: "player"},
		Grant{UserSub: defaultsSub, ServerID: defaultsSub, Role: "player"},
	)
	store := NewStore(table, "grants")

	if err := store.ApplyDefaultsTo(context.Background(), "newserver"); err != nil {
		t.Fatal(err)
	}

	for _, g := range table.rows() {
		if g.UserSub == defaultsSub && g.ServerID == "newserver" {
			t.Error("the defaults row was granted access to a server")
		}
	}
}

func TestApplyDefaultsToGrantsNothingWithoutADefaultRole(t *testing.T) {
	table := newFakeTable(
		Grant{UserSub: "alice", ServerID: "valheim", Role: "player"},
		Grant{UserSub: defaultsSub, ServerID: defaultsSub, Role: ""},
	)
	store := NewStore(table, "grants")

	if err := store.ApplyDefaultsTo(context.Background(), "newserver"); err != nil {
		t.Fatal(err)
	}

	for _, g := range table.rows() {
		if g.ServerID == "newserver" {
			t.Errorf("%s was granted %q on a new server with no default role", g.UserSub, g.Role)
		}
	}
}
