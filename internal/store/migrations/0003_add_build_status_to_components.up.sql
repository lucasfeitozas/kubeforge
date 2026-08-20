ALTER TABLE components ADD COLUMN phase TEXT NOT NULL DEFAULT 'Pending'
    CHECK (phase IN ('Pending', 'Building', 'Built', 'Running', 'Succeeded', 'Failed', 'CleanedUp'));
ALTER TABLE components ADD COLUMN build_image_digest TEXT;
ALTER TABLE components ADD COLUMN error_message TEXT;
