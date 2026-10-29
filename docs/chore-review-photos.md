# Chore review photos

Chore review photos are stored as private Tigris objects. MongoDB stores only
the object key for each review round; normal chore responses expose the rounds
that have a photo, not the object keys.

## Deployment

Tigris configuration is required when the API process starts:

- `AWS_ENDPOINT_URL_S3`
- `AWS_REGION`
- `BUCKET_NAME`
- `AWS_ACCESS_KEY_ID`
- `AWS_SECRET_ACCESS_KEY`

For the Fly.io app, run `fly storage create`. Fly attaches the bucket by setting
these five values as app secrets. Provision storage before deploying this
version. The application does not send a public ACL while uploading; the
bucket policy must also remain private.

For local Docker Compose, export the same variables in the shell or provide
them through a local `.env` file. The API service forwards them into the
container. The migration service does not need Tigris credentials.

Recommended rollout order:

1. Create and attach the private Tigris bucket.
2. Run migrations `0055`, `0056`, and `0057` through the normal release command.
3. Deploy the API.
4. Enable `isChoreReviewPhotoRequired` for selected houses.

## Upload contract

`PUT /api/v1/chore/status` continues to accept its existing JSON body. A photo
upload uses `multipart/form-data` with:

- `houseId`: house ID
- `chores`: JSON-encoded array containing exactly one `InTest` transition
- `file`: JPEG, PNG, or WebP image, at most 3 MB

Example:

```bash
curl -X PUT "$API_URL/api/v1/chore/status" \
  -H "Authorization: Bearer $TOKEN" \
  -F "houseId=$HOUSE_ID" \
  -F 'chores=[{"choreId":"CHORE_ID","status":2}]' \
  -F "file=@review-photo.jpg"
```

When the house setting requires a photo, a JSON-only `InTest` transition is
rejected. The upload completes before the status response is returned. If the
MongoDB transaction fails after upload, the uncommitted Tigris object is
deleted.

## Viewing contract

`GET /api/v1/chore/{choreId}/review-photo/{round}` verifies that the requester
is still a member of the chore's house, then returns:

```json
{
  "url": "https://...signed-url...",
  "expiresAt": "2026-10-29T10:20:00Z"
}
```

The URL expires after five minutes. The response uses
`Cache-Control: private, no-store`. Clients should request a new URL instead of
persisting or sharing it.

## Operational checks

- Upload one photo and confirm the object is private when requested without a
  signature.
- Confirm a current house member can obtain and open the presigned URL.
- Confirm a non-member receives `403` and a round without a photo receives
  `404`.
- Keep bucket credentials in Fly secrets; do not add them to `fly.toml`, local
  config files, logs, or client applications.
