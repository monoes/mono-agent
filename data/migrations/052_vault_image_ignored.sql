-- Image files in a profile's folder that the user removed from the image
-- vault but kept on disk (`image delete` of a discovered or in-place
-- image). `image sync` skips these paths so a deleted image does not come
-- straight back; registering the path again (`image add <path>`) clears it.
CREATE TABLE IF NOT EXISTS vault_image_ignored (
    profile_id TEXT NOT NULL,
    path       TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (profile_id, path)
);
