package communication

import "context"

type MessageHandler func(ctx context.Context, msg Message) ([]byte, error)
