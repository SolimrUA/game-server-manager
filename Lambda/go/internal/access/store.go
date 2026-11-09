package access

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	keyUserSub = "userSub"
	keyServer  = "serverId"
)

// DynamoDB is the grants table.
type DynamoDB interface {
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	BatchWriteItem(context.Context, *dynamodb.BatchWriteItemInput, ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
}

// Store reads and writes who may do what to which server.
type Store struct {
	db    DynamoDB
	table string
}

func NewStore(db DynamoDB, table string) *Store {
	return &Store{db: db, table: table}
}

// Grant is one user's access to one server.
type Grant struct {
	UserSub  string `json:"userSub"`
	ServerID string `json:"serverId"`
	Role     string `json:"role"`
}

// Access is everything one caller has been granted.
type Access struct {
	// Roles is the template each server grants them, keyed by server id.
	Roles map[string]string
	// CanCreate reports whether they hold an unspent permission to create a server.
	CanCreate bool
	// Seen reports whether anyone has decided anything about this caller yet. Taking someone's access
	// away records that it was taken, so an empty partition means a caller nobody has met.
	Seen bool
}

// RolesFor reads what a caller has been granted. One query serves a whole request: the permit is an
// item in the same partition.
func (s *Store) RolesFor(ctx context.Context, userSub string) (Access, error) {
	if s.table == "" || userSub == "" {
		return Access{}, nil
	}

	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(s.table),
		KeyConditionExpression:    aws.String(keyUserSub + " = :sub"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":sub": &ddbtypes.AttributeValueMemberS{Value: userSub}},
	})
	if err != nil {
		return Access{}, fmt.Errorf("reading access for %s: %w", userSub, err)
	}

	got := Access{Roles: make(map[string]string, len(out.Items)), Seen: len(out.Items) > 0}
	for _, item := range out.Items {
		if server := stringAttr(item, keyServer); server == permitKey {
			got.CanCreate = boolAttr(item, permitAttr)
		} else {
			got.Roles[server] = stringAttr(item, "role")
		}
	}
	return got, nil
}

// All returns every grant, for the access page.
func (s *Store) All(ctx context.Context) ([]Grant, error) {
	if s.table == "" {
		return nil, nil
	}

	grants := []Grant{}
	pages := dynamodb.NewScanPaginator(s.db, &dynamodb.ScanInput{TableName: aws.String(s.table)})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("scanning access grants: %w", err)
		}
		for _, item := range page.Items {
			// The permit shares the table but is not access to a server, and the access page's grid
			// would otherwise show it as one.
			if stringAttr(item, keyServer) == permitKey {
				continue
			}
			grants = append(grants, Grant{
				UserSub:  stringAttr(item, keyUserSub),
				ServerID: stringAttr(item, keyServer),
				Role:     stringAttr(item, "role"),
			})
		}
	}
	return grants, nil
}

// Change is one requested grant. An empty Role means no access.
type Change struct {
	UserSub  string `json:"userSub"`
	ServerID string `json:"serverId"`
	Role     string `json:"role"`
	Email    string `json:"email"`
}

// Save applies changes. A change to no access stores an empty role rather than deleting the item, so
// a user with no items at all is one nobody has decided anything about.
func (s *Store) Save(ctx context.Context, changes []Change, callerSub string) error {
	if s.table == "" {
		return fmt.Errorf("access storage isn't configured")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	writes := make([]ddbtypes.WriteRequest, 0, len(changes))
	for _, c := range changes {
		if c.UserSub == "" || c.ServerID == "" {
			return fmt.Errorf("a change was missing its user or server")
		}
		if c.Role != "" && !KnownTemplate(c.Role) {
			return fmt.Errorf("unknown access template: %s", c.Role)
		}

		key := map[string]ddbtypes.AttributeValue{
			keyUserSub: &ddbtypes.AttributeValueMemberS{Value: c.UserSub},
			keyServer:  &ddbtypes.AttributeValueMemberS{Value: c.ServerID},
		}
		item := map[string]ddbtypes.AttributeValue{
			keyUserSub:  key[keyUserSub],
			keyServer:   key[keyServer],
			"role":      &ddbtypes.AttributeValueMemberS{Value: c.Role},
			"updatedAt": &ddbtypes.AttributeValueMemberS{Value: now},
		}
		if callerSub != "" {
			item["updatedBy"] = &ddbtypes.AttributeValueMemberS{Value: callerSub}
		}
		if c.Email != "" {
			item["email"] = &ddbtypes.AttributeValueMemberS{Value: c.Email}
		}
		writes = append(writes, ddbtypes.WriteRequest{PutRequest: &ddbtypes.PutRequest{Item: item}})
	}

	return s.write(ctx, writes)
}

// write sends the requests in batches of 25, which is BatchWriteItem's limit. Declined writes are
// reported rather than failed, so they are retried before anything is called saved.
func (s *Store) write(ctx context.Context, writes []ddbtypes.WriteRequest) error {
	const batchSize = 25
	for start := 0; start < len(writes); start += batchSize {
		batch := writes[start:min(start+batchSize, len(writes))]

		pending := map[string][]ddbtypes.WriteRequest{s.table: batch}
		for attempt := 0; len(pending[s.table]) > 0; attempt++ {
			if attempt == 4 {
				return fmt.Errorf("saved some changes, but %d couldn't be written - reload and check",
					len(pending[s.table]))
			}
			if attempt > 0 {
				time.Sleep(time.Second)
			}
			out, err := s.db.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: pending})
			if err != nil {
				return fmt.Errorf("saving access: %w", err)
			}
			pending = out.UnprocessedItems
		}
	}
	return nil
}

func stringAttr(item map[string]ddbtypes.AttributeValue, name string) string {
	if value, ok := item[name].(*ddbtypes.AttributeValueMemberS); ok {
		return value.Value
	}
	return ""
}

// permitKey is the sort key of the item holding a user's permission to create one server. A server id
// can hold no '#', so this can never collide with one.
const permitKey = "#create-permit"

const permitAttr = "canCreate"

// defaultsSub is the partition holding the defaults themselves. A Cognito sub can hold no '#', so it
// can never collide with a user's own.
const defaultsSub = "#defaults"

// Permits reports which users may create a server, keyed by sub. Only users holding one appear.
func (s *Store) Permits(ctx context.Context) (map[string]bool, error) {
	if s.table == "" {
		return nil, nil
	}

	permits := map[string]bool{}
	pages := dynamodb.NewScanPaginator(s.db, &dynamodb.ScanInput{
		TableName:                 aws.String(s.table),
		FilterExpression:          aws.String(keyServer + " = :key"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":key": &ddbtypes.AttributeValueMemberS{Value: permitKey}},
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("scanning create permits: %w", err)
		}
		for _, item := range page.Items {
			if boolAttr(item, permitAttr) {
				permits[stringAttr(item, keyUserSub)] = true
			}
		}
	}
	return permits, nil
}

// SetPermit gives a user permission to create one server, or takes it back.
func (s *Store) SetPermit(ctx context.Context, userSub string, allowed bool, callerSub string) error {
	if s.table == "" {
		return fmt.Errorf("access storage isn't configured")
	}
	if userSub == "" {
		return fmt.Errorf("a permit change was missing its user")
	}

	item := map[string]ddbtypes.AttributeValue{
		keyUserSub: &ddbtypes.AttributeValueMemberS{Value: userSub},
		keyServer:  &ddbtypes.AttributeValueMemberS{Value: permitKey},
		permitAttr: &ddbtypes.AttributeValueMemberBOOL{Value: allowed},
		"updatedAt": &ddbtypes.AttributeValueMemberS{
			Value: time.Now().UTC().Format(time.RFC3339)},
	}
	if callerSub != "" {
		item["updatedBy"] = &ddbtypes.AttributeValueMemberS{Value: callerSub}
	}

	_, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.table), Item: item})
	if err != nil {
		return fmt.Errorf("storing the create permit for %s: %w", userSub, err)
	}
	return nil
}

// ConsumePermit spends a user's permission to create a server, and reports whether they had one.
// The condition is what stops two requests at once from spending the same permit twice.
func (s *Store) ConsumePermit(ctx context.Context, userSub string) (bool, error) {
	if s.table == "" {
		return false, fmt.Errorf("access storage isn't configured")
	}

	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			keyUserSub: &ddbtypes.AttributeValueMemberS{Value: userSub},
			keyServer:  &ddbtypes.AttributeValueMemberS{Value: permitKey},
		},
		UpdateExpression:    aws.String("SET " + permitAttr + " = :no"),
		ConditionExpression: aws.String(permitAttr + " = :yes"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":no":  &ddbtypes.AttributeValueMemberBOOL{Value: false},
			":yes": &ddbtypes.AttributeValueMemberBOOL{Value: true},
		},
	})
	var unmet *ddbtypes.ConditionalCheckFailedException
	if errors.As(err, &unmet) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("spending the create permit for %s: %w", userSub, err)
	}
	return true, nil
}

func boolAttr(item map[string]ddbtypes.AttributeValue, name string) bool {
	if value, ok := item[name].(*ddbtypes.AttributeValueMemberBOOL); ok {
		return value.Value
	}
	return false
}

// Defaults are what a caller is granted the first time they appear, written as ordinary grants.
type Defaults struct {
	// Role is the template every server grants a new caller, empty for none.
	Role      string `json:"role"`
	CanCreate bool   `json:"canCreate"`
}

// Defaults reads what new callers are granted.
func (s *Store) Defaults(ctx context.Context) (Defaults, error) {
	if s.table == "" {
		return Defaults{}, nil
	}

	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(s.table),
		KeyConditionExpression:    aws.String(keyUserSub + " = :sub"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":sub": &ddbtypes.AttributeValueMemberS{Value: defaultsSub}},
	})
	if err != nil {
		return Defaults{}, fmt.Errorf("reading the access defaults: %w", err)
	}
	if len(out.Items) == 0 {
		return Defaults{}, nil
	}
	return Defaults{Role: stringAttr(out.Items[0], "role"), CanCreate: boolAttr(out.Items[0], permitAttr)}, nil
}

// SetDefaults changes what new callers are granted. It does not touch anyone already granted.
func (s *Store) SetDefaults(ctx context.Context, d Defaults, callerSub string) error {
	if s.table == "" {
		return fmt.Errorf("access storage isn't configured")
	}
	if d.Role != "" && d.Role != string(Player) {
		return fmt.Errorf("a new user can be granted %s or nothing, not %s", Player, d.Role)
	}

	item := map[string]ddbtypes.AttributeValue{
		keyUserSub:  &ddbtypes.AttributeValueMemberS{Value: defaultsSub},
		keyServer:   &ddbtypes.AttributeValueMemberS{Value: defaultsSub},
		"role":      &ddbtypes.AttributeValueMemberS{Value: d.Role},
		permitAttr:  &ddbtypes.AttributeValueMemberBOOL{Value: d.CanCreate},
		"updatedAt": &ddbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
	}
	if callerSub != "" {
		item["updatedBy"] = &ddbtypes.AttributeValueMemberS{Value: callerSub}
	}

	_, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.table), Item: item})
	if err != nil {
		return fmt.Errorf("saving the access defaults: %w", err)
	}
	return nil
}

// Seed applies the defaults to one caller. What it writes is what makes them a caller somebody has
// met, so it never runs twice.
func (s *Store) Seed(ctx context.Context, userSub string, serverIDs []string, d Defaults) error {
	if s.table == "" || userSub == "" {
		return nil
	}

	changes := make([]Change, 0, len(serverIDs))
	if d.Role != "" {
		for _, id := range serverIDs {
			changes = append(changes, Change{UserSub: userSub, ServerID: id, Role: d.Role})
		}
	}
	if err := s.Save(ctx, changes, ""); err != nil {
		return err
	}
	if d.CanCreate {
		if err := s.SetPermit(ctx, userSub, true, ""); err != nil {
			return err
		}
	}

	return nil
}

// ForgetServer deletes a server's grants outright, where revoking access keeps an empty role. A
// server id can be used again, and the next server to take it would otherwise inherit the access the
// last one was given.
func (s *Store) ForgetServer(ctx context.Context, serverID string) error {
	if s.table == "" || serverID == "" {
		return nil
	}

	keys, err := s.keysForServer(ctx, serverID)
	if err != nil {
		return err
	}
	writes := make([]ddbtypes.WriteRequest, 0, len(keys))
	for _, key := range keys {
		writes = append(writes, ddbtypes.WriteRequest{DeleteRequest: &ddbtypes.DeleteRequest{Key: key}})
	}
	return s.write(ctx, writes)
}

// keysForServer scans because the table is keyed by user, not by server.
func (s *Store) keysForServer(ctx context.Context, serverID string) ([]map[string]ddbtypes.AttributeValue, error) {
	keys := []map[string]ddbtypes.AttributeValue{}
	pages := dynamodb.NewScanPaginator(s.db, &dynamodb.ScanInput{
		TableName:                 aws.String(s.table),
		ProjectionExpression:      aws.String("#user, #server"),
		FilterExpression:          aws.String("#server = :server"),
		ExpressionAttributeNames:  map[string]string{"#user": keyUserSub, "#server": keyServer},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":server": &ddbtypes.AttributeValueMemberS{Value: serverID}},
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("scanning the grants on %s: %w", serverID, err)
		}
		for _, item := range page.Items {
			keys = append(keys, map[string]ddbtypes.AttributeValue{
				keyUserSub: &ddbtypes.AttributeValueMemberS{Value: stringAttr(item, keyUserSub)},
				keyServer:  &ddbtypes.AttributeValueMemberS{Value: stringAttr(item, keyServer)},
			})
		}
	}
	return keys, nil
}

// ApplyDefaultsTo makes a new server reachable by the people the whole fleet is already shared with,
// not only by whoever created it.
func (s *Store) ApplyDefaultsTo(ctx context.Context, serverID string) error {
	if s.table == "" || serverID == "" {
		return nil
	}

	d, err := s.Defaults(ctx)
	if err != nil {
		return err
	}
	if d.Role == "" {
		return nil
	}

	grants, err := s.All(ctx)
	if err != nil {
		return err
	}
	changes := []Change{}
	for _, sub := range subsWithAccessEverywhere(grants, serverID) {
		changes = append(changes, Change{UserSub: sub, ServerID: serverID, Role: d.Role})
	}
	return s.Save(ctx, changes, "")
}

// subsWithAccessEverywhere demands a role on every other server because someone kept off an existing
// server is not who a new one is shared with by default.
func subsWithAccessEverywhere(grants []Grant, exclude string) []string {
	servers := map[string]bool{}
	held := map[string]int{}
	order := []string{}
	for _, g := range grants {
		if g.ServerID == exclude || g.UserSub == defaultsSub {
			continue
		}
		servers[g.ServerID] = true
		if g.Role == "" {
			continue
		}
		if held[g.UserSub] == 0 {
			order = append(order, g.UserSub)
		}
		held[g.UserSub]++
	}

	subs := []string{}
	for _, sub := range order {
		if held[sub] == len(servers) {
			subs = append(subs, sub)
		}
	}
	return subs
}
