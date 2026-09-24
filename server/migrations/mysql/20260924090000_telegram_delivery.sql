ALTER TABLE notification_logs ADD COLUMN delivery_key varchar(128) NULL;
ALTER TABLE notification_logs ADD COLUMN attempts integer NOT NULL DEFAULT 0;
ALTER TABLE notification_logs ADD COLUMN next_attempt_at datetime(6) NULL;
ALTER TABLE notification_logs ADD COLUMN lease_until datetime(6) NULL;
ALTER TABLE notification_logs ADD COLUMN message_id varchar(64) NULL;
CREATE UNIQUE INDEX notification_logs_delivery_key ON notification_logs (delivery_key);
