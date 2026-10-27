package access

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Grants are keyed on the server id: a server rebuilt under the same id is the same server to the
// people playing on it, and keeps the access they were given.
const (
	keyUserSub = "userSub"
	keyServer  = "serverId"
)

// DynamoDB is the grants table.
type DynamoDB interface {
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
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

// RolesFor returns the template each server grants this caller, keyed by server id. One query serves
// a whole request.
func (s *Store) RolesFor(ctx context.Context, userSub string) (map[string]string, error) {
	if s.table == "" || userSub == "" {
		return nil, nil
	}

	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(s.table),
		KeyConditionExpression:    aws.String(keyUserSub + " = :sub"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":sub": &ddbtypes.AttributeValueMemberS{Value: userSub}},
	})
	if err != nil {
		return nil, fmt.Errorf("reading access for %s: %w", userSub, err)
	}

	roles := make(map[string]string, len(out.Items))
	for _, item := range out.Items {
		roles[stringAttr(item, keyServer)] = stringAttr(item, "role")
	}
	return roles, nil
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

// Save applies changes. A change to no access is a delete rather than a stored empty value, so the
// table only ever holds real grants.
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
		if c.Role == "" {
			writes = append(writes, ddbtypes.WriteRequest{
				DeleteRequest: &ddbtypes.DeleteRequest{Key: key},
			})
			continue
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
