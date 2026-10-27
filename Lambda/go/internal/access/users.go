package access

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cognitotypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)

// adminGroup names the Cognito group whose members may manage servers and access.
const adminGroup = "admins"

// Cognito holds the accounts. It stays the only record of who exists; the grants table holds
// nothing but grants.
type Cognito interface {
	ListUsers(context.Context, *cognitoidentityprovider.ListUsersInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.ListUsersOutput, error)
	ListUsersInGroup(context.Context, *cognitoidentityprovider.ListUsersInGroupInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.ListUsersInGroupOutput, error)
	AdminUpdateUserAttributes(context.Context, *cognitoidentityprovider.AdminUpdateUserAttributesInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.AdminUpdateUserAttributesOutput, error)
}

// Directory is the user pool the control panel grants access to.
type Directory struct {
	cognito Cognito
	poolID  string
}

func NewDirectory(cognito Cognito, poolID string) *Directory {
	return &Directory{cognito: cognito, poolID: poolID}
}

// User is an account the access page can grant a server to.
type User struct {
	// Username is Cognito's own id for the user, which is what a rename has to address - with a pool
	// keyed on email it is a generated UUID rather than anything readable.
	Username string `json:"username"`
	Sub      string `json:"sub"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	IsAdmin  bool   `json:"isAdmin"`
}

// Users lists every account, admins first and then by display name.
func (d *Directory) Users(ctx context.Context) ([]User, error) {
	if d.poolID == "" {
		return nil, nil
	}

	admins, err := d.admins(ctx)
	if err != nil {
		return nil, err
	}

	users := []User{}
	pages := cognitoidentityprovider.NewListUsersPaginator(d.cognito,
		&cognitoidentityprovider.ListUsersInput{UserPoolId: aws.String(d.poolID)})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing users: %w", err)
		}
		for _, u := range page.Users {
			attrs := attributes(u.Attributes)
			sub := attrs["sub"]
			if sub == "" {
				continue
			}
			users = append(users, User{
				Username: aws.ToString(u.Username),
				Sub:      sub,
				Email:    attrs["email"],
				Name:     attrs["name"],
				IsAdmin:  admins[sub],
			})
		}
	}

	sort.Slice(users, func(i, j int) bool {
		if users[i].IsAdmin != users[j].IsAdmin {
			return users[i].IsAdmin
		}
		return users[i].sortName() < users[j].sortName()
	})
	return users, nil
}

func (u User) sortName() string {
	if u.Name != "" {
		return strings.ToLower(u.Name)
	}
	return strings.ToLower(u.Email)
}

// admins returns the subs in the admins group. One call for the whole group, rather than asking per
// user which groups they are in.
func (d *Directory) admins(ctx context.Context) (map[string]bool, error) {
	subs := map[string]bool{}
	pages := cognitoidentityprovider.NewListUsersInGroupPaginator(d.cognito,
		&cognitoidentityprovider.ListUsersInGroupInput{
			UserPoolId: aws.String(d.poolID),
			GroupName:  aws.String(adminGroup),
		})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing the %s group: %w", adminGroup, err)
		}
		for _, u := range page.Users {
			if sub := attributes(u.Attributes)["sub"]; sub != "" {
				subs[sub] = true
			}
		}
	}
	return subs, nil
}

// Name is a requested display name for a user.
type Name struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

// Rename writes display names back to Cognito's own "name" attribute, so there is no second copy to
// drift. It is a standard optional attribute, so this needs no change to the pool's schema. Returns
// how many were renamed.
func (d *Directory) Rename(ctx context.Context, names []Name) (int, error) {
	if d.poolID == "" {
		return 0, nil
	}

	renamed := 0
	var failures []string
	for _, n := range names {
		if n.Username == "" {
			continue
		}
		_, err := d.cognito.AdminUpdateUserAttributes(ctx, &cognitoidentityprovider.AdminUpdateUserAttributesInput{
			UserPoolId: aws.String(d.poolID),
			Username:   aws.String(n.Username),
			UserAttributes: []cognitotypes.AttributeType{{
				Name:  aws.String("name"),
				Value: aws.String(strings.TrimSpace(n.Name)),
			}},
		})
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", n.Username, err))
			continue
		}
		renamed++
	}
	if len(failures) > 0 {
		return renamed, fmt.Errorf("couldn't rename %s", strings.Join(failures, "; "))
	}
	return renamed, nil
}

func attributes(attrs []cognitotypes.AttributeType) map[string]string {
	byName := make(map[string]string, len(attrs))
	for _, a := range attrs {
		byName[aws.ToString(a.Name)] = aws.ToString(a.Value)
	}
	return byName
}

// Groups reads the caller's groups as API Gateway's mapping template renders them. The multi-valued
// cognito:groups claim arrives as "[admins, users]" rather than JSON, so the brackets are stripped
// instead of assuming a clean comma list.
func Groups(claim string) []string {
	var groups []string
	for _, g := range strings.Split(strings.Trim(strings.TrimSpace(claim), "[]"), ",") {
		if g = strings.TrimSpace(g); g != "" {
			groups = append(groups, g)
		}
	}
	return groups
}

// IsAdmin reports whether the caller's groups make them an administrator.
func IsAdmin(groups []string) bool {
	for _, g := range groups {
		if g == adminGroup {
			return true
		}
	}
	return false
}
