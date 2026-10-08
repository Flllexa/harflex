package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

const channelColumns = "id,workspace_id,name,folder,status,last_error,inbox_error,outbox_error,created_at,updated_at"
const channelMessageColumns = "id,channel_id,request_id,direction,file_name,content_hash,content,status,error_code,created_at,updated_at"

func scanLocalChannel(row rowScanner) (catalog.LocalChannel, error) {
	var item catalog.LocalChannel
	var created, updated string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.Name, &item.Folder, &item.Status, &item.LastError, &item.InboxError, &item.OutboxError, &created, &updated); err != nil {
		return item, err
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return item, fmt.Errorf("parse channel created time: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return item, fmt.Errorf("parse channel updated time: %w", err)
	}
	return item, nil
}

func scanChannelMessage(row rowScanner) (catalog.ChannelMessage, error) {
	var item catalog.ChannelMessage
	var created, updated string
	if err := row.Scan(&item.ID, &item.ChannelID, &item.RequestID, &item.Direction, &item.FileName, &item.ContentHash, &item.Content, &item.Status, &item.ErrorCode, &created, &updated); err != nil {
		return item, err
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return item, fmt.Errorf("parse message created time: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return item, fmt.Errorf("parse message updated time: %w", err)
	}
	return item, nil
}

func (s *Store) SaveLocalChannel(ctx context.Context, item catalog.LocalChannel) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO local_channels(id,workspace_id,name,folder,status,last_error,inbox_error,outbox_error,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,folder=excluded.folder,status=excluded.status,last_error=excluded.last_error,inbox_error=excluded.inbox_error,outbox_error=excluded.outbox_error,updated_at=excluded.updated_at
		WHERE local_channels.workspace_id=excluded.workspace_id`, item.ID, item.WorkspaceID, item.Name, item.Folder, item.Status, item.LastError, item.InboxError, item.OutboxError, formatCatalogTime(item.CreatedAt), formatCatalogTime(item.UpdatedAt))
	if err != nil {
		return fmt.Errorf("save local channel: %w", err)
	}
	return nil
}

func (s *Store) GetLocalChannel(ctx context.Context, id string) (catalog.LocalChannel, error) {
	item, err := scanLocalChannel(s.db.QueryRowContext(ctx, "SELECT "+channelColumns+" FROM local_channels WHERE id=?", id))
	if err != nil {
		return item, fmt.Errorf("get local channel: %w", err)
	}
	return item, nil
}

func (s *Store) GetLocalChannelByFolder(ctx context.Context, workspaceID, folder string) (catalog.LocalChannel, error) {
	item, err := scanLocalChannel(s.db.QueryRowContext(ctx, "SELECT "+channelColumns+" FROM local_channels WHERE workspace_id=? AND folder=?", workspaceID, folder))
	if err != nil {
		return item, fmt.Errorf("get local channel folder: %w", err)
	}
	return item, nil
}

func (s *Store) ListLocalChannels(ctx context.Context, workspaceID string) ([]catalog.LocalChannel, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+channelColumns+" FROM local_channels WHERE workspace_id=? ORDER BY updated_at DESC,id", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list local channels: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.LocalChannel, 0)
	for rows.Next() {
		item, err := scanLocalChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("scan local channel: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate local channels: %w", err)
	}
	return items, nil
}

func (s *Store) SetLocalChannelStatus(ctx context.Context, channelID, side, errorCode string) (catalog.LocalChannel, error) {
	if side != "inbox" && side != "outbox" {
		return catalog.LocalChannel{}, fmt.Errorf("invalid channel status side")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return catalog.LocalChannel{}, fmt.Errorf("begin channel status: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE local_channels SET inbox_error=CASE WHEN ?='inbox' THEN ? ELSE inbox_error END,
		outbox_error=CASE WHEN ?='outbox' THEN ? ELSE outbox_error END,updated_at=? WHERE id=?`, side, errorCode, side, errorCode, formatCatalogTime(now), channelID); err != nil {
		return catalog.LocalChannel{}, fmt.Errorf("set channel status: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE local_channels SET status=CASE WHEN inbox_error!='' OR outbox_error!='' THEN 'error' ELSE 'ready' END,
		last_error=CASE WHEN inbox_error!='' THEN inbox_error ELSE outbox_error END WHERE id=?`, channelID); err != nil {
		return catalog.LocalChannel{}, fmt.Errorf("derive channel status: %w", err)
	}
	item, err := scanLocalChannel(tx.QueryRowContext(ctx, "SELECT "+channelColumns+" FROM local_channels WHERE id=?", channelID))
	if err != nil {
		return catalog.LocalChannel{}, fmt.Errorf("read channel status: %w", err)
	}
	if err := appendChannelEvent(ctx, tx, channelID, "", side, item.Status, errorCode, now); err != nil {
		return catalog.LocalChannel{}, err
	}
	if err := tx.Commit(); err != nil {
		return catalog.LocalChannel{}, fmt.Errorf("commit channel status: %w", err)
	}
	return item, nil
}

func appendChannelEvent(ctx context.Context, tx *sql.Tx, channelID, messageID, kind, status, code string, now time.Time) error {
	var ref any
	if messageID != "" {
		ref = messageID
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO channel_events(channel_id,message_id,kind,status,error_code,created_at) VALUES(?,?,?,?,?,?)", channelID, ref, kind, status, code, formatCatalogTime(now))
	if err != nil {
		return fmt.Errorf("append channel event: %w", err)
	}
	return nil
}

func (s *Store) AddIncomingChannelMessage(ctx context.Context, item catalog.ChannelMessage) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin incoming message: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO channel_messages(id,channel_id,request_id,direction,file_name,content_hash,content,status,error_code,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(channel_id,direction,file_name,content_hash) DO NOTHING`, item.ID, item.ChannelID, item.RequestID, item.Direction, item.FileName, item.ContentHash, item.Content, item.Status, item.ErrorCode, formatCatalogTime(item.CreatedAt), formatCatalogTime(item.UpdatedAt))
	if err != nil {
		return false, fmt.Errorf("insert incoming message: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count incoming insert: %w", err)
	}
	if count != 0 {
		if err := appendChannelEvent(ctx, tx, item.ChannelID, item.ID, "import", item.Status, item.ErrorCode, item.CreatedAt); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit incoming message: %w", err)
	}
	return count != 0, nil
}

func (s *Store) ReserveOutgoingChannelMessage(ctx context.Context, item catalog.ChannelMessage) (catalog.ChannelMessage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return item, fmt.Errorf("begin outgoing message: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_messages(id,channel_id,request_id,direction,file_name,content_hash,content,status,error_code,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(channel_id,request_id) WHERE request_id != '' DO NOTHING`, item.ID, item.ChannelID, item.RequestID, item.Direction, item.FileName, item.ContentHash, item.Content, item.Status, item.ErrorCode, formatCatalogTime(item.CreatedAt), formatCatalogTime(item.UpdatedAt))
	if err != nil {
		return item, fmt.Errorf("reserve outgoing message: %w", err)
	}
	stored, err := scanChannelMessage(tx.QueryRowContext(ctx, "SELECT "+channelMessageColumns+" FROM channel_messages WHERE channel_id=? AND request_id=?", item.ChannelID, item.RequestID))
	if err != nil {
		return item, fmt.Errorf("read outgoing reservation: %w", err)
	}
	if stored.ID == item.ID {
		if err := appendChannelEvent(ctx, tx, item.ChannelID, item.ID, "send", "pending", "", item.CreatedAt); err != nil {
			return item, err
		}
	}
	if err := tx.Commit(); err != nil {
		return item, fmt.Errorf("commit outgoing reservation: %w", err)
	}
	return stored, nil
}

func (s *Store) GetOutgoingChannelMessage(ctx context.Context, channelID, requestID string) (catalog.ChannelMessage, error) {
	item, err := scanChannelMessage(s.db.QueryRowContext(ctx, "SELECT "+channelMessageColumns+" FROM channel_messages WHERE channel_id=? AND request_id=? AND direction='outgoing'", channelID, requestID))
	if err != nil {
		return item, fmt.Errorf("get outgoing channel message: %w", err)
	}
	return item, nil
}

func (s *Store) SetChannelMessageStatus(ctx context.Context, channelID, messageID, status, errorCode string) (catalog.ChannelMessage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return catalog.ChannelMessage{}, fmt.Errorf("begin message status: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, "UPDATE channel_messages SET status=?,error_code=?,updated_at=? WHERE channel_id=? AND id=?", status, errorCode, formatCatalogTime(now), channelID, messageID)
	if err != nil {
		return catalog.ChannelMessage{}, fmt.Errorf("update message status: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return catalog.ChannelMessage{}, fmt.Errorf("count message update: %w", err)
	}
	if count == 0 {
		return catalog.ChannelMessage{}, sql.ErrNoRows
	}
	if err := appendChannelEvent(ctx, tx, channelID, messageID, "delivery", status, errorCode, now); err != nil {
		return catalog.ChannelMessage{}, err
	}
	item, err := scanChannelMessage(tx.QueryRowContext(ctx, "SELECT "+channelMessageColumns+" FROM channel_messages WHERE channel_id=? AND id=?", channelID, messageID))
	if err != nil {
		return catalog.ChannelMessage{}, fmt.Errorf("read message status: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return catalog.ChannelMessage{}, fmt.Errorf("commit message status: %w", err)
	}
	return item, nil
}

func (s *Store) ListChannelMessages(ctx context.Context, channelID string) ([]catalog.ChannelMessage, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+channelMessageColumns+" FROM channel_messages WHERE channel_id=? ORDER BY created_at DESC,id DESC LIMIT 500", channelID)
	if err != nil {
		return nil, fmt.Errorf("list channel messages: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.ChannelMessage, 0)
	for rows.Next() {
		item, err := scanChannelMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan channel message: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate channel messages: %w", err)
	}
	return items, nil
}
