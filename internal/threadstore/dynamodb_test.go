package threadstore

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeDynamoAPI struct {
	items map[string]map[string]types.AttributeValue
	err   error
}

func newFakeDynamoAPI() *fakeDynamoAPI {
	return &fakeDynamoAPI{items: map[string]map[string]types.AttributeValue{}}
}

func (f *fakeDynamoAPI) GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	key := params.Key[pkAttr].(*types.AttributeValueMemberS).Value
	return &dynamodb.GetItemOutput{Item: f.items[key]}, nil
}

func (f *fakeDynamoAPI) PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	key := params.Item[pkAttr].(*types.AttributeValueMemberS).Value
	f.items[key] = params.Item
	return &dynamodb.PutItemOutput{}, nil
}

func TestDynamoDBStore_GetMiss(t *testing.T) {
	store := &DynamoDBStore{client: newFakeDynamoAPI(), tableName: "threads"}

	_, found, err := store.Get(context.Background(), "acme/widgets#42")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if found {
		t.Error("expected found=false for an unseen key")
	}
}

func TestDynamoDBStore_PutThenGet(t *testing.T) {
	store := &DynamoDBStore{client: newFakeDynamoAPI(), tableName: "threads"}
	ctx := context.Background()

	if err := store.Put(ctx, "acme/widgets#42", "1111.2222"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	ts, found, err := store.Get(ctx, "acme/widgets#42")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !found {
		t.Fatal("expected found=true after Put")
	}
	if ts != "1111.2222" {
		t.Errorf("ts = %q, want 1111.2222", ts)
	}
}

func TestDynamoDBStore_GetPropagatesError(t *testing.T) {
	api := newFakeDynamoAPI()
	api.err = errors.New("throttled")
	store := &DynamoDBStore{client: api, tableName: "threads"}

	if _, _, err := store.Get(context.Background(), "acme/widgets#42"); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestKey(t *testing.T) {
	if got, want := Key("acme", "widgets", 42), "acme/widgets#42"; got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}
