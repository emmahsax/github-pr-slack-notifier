package threadstore

import (
	"context"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	pkAttr        = "pr_key"
	threadTSAttr  = "thread_ts"
	expiresAtAttr = "expires_at"
	// ttl bounds table growth automatically via the table's configured TTL
	// attribute; a PR whose thread goes untouched for this long is assumed
	// to be inactive.
	ttl = 90 * 24 * time.Hour
)

// dynamoAPI is the subset of *dynamodb.Client this package calls, so tests
// can substitute a fake instead of hitting real AWS.
type dynamoAPI interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

// DynamoDBStore implements Store against a single DynamoDB table with a
// string partition key (pkAttr) and a TTL attribute (expiresAtAttr).
type DynamoDBStore struct {
	client    dynamoAPI
	tableName string
}

func NewDynamoDBStore(client *dynamodb.Client, tableName string) *DynamoDBStore {
	return &DynamoDBStore{client: client, tableName: tableName}
}

func (s *DynamoDBStore) Get(ctx context.Context, key string) (string, bool, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			pkAttr: &types.AttributeValueMemberS{Value: key},
		},
	})
	if err != nil {
		return "", false, err
	}
	if out.Item == nil {
		return "", false, nil
	}
	tsAttr, ok := out.Item[threadTSAttr].(*types.AttributeValueMemberS)
	if !ok {
		return "", false, nil
	}
	return tsAttr.Value, true, nil
}

func (s *DynamoDBStore) Put(ctx context.Context, key, threadTS string) error {
	_, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item: map[string]types.AttributeValue{
			pkAttr:        &types.AttributeValueMemberS{Value: key},
			threadTSAttr:  &types.AttributeValueMemberS{Value: threadTS},
			expiresAtAttr: &types.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)},
		},
	})
	return err
}
