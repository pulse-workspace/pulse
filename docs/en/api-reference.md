# REST API Reference

The Pulse REST API provides programmatic control over authentication, user accounts, groups, conversations, friendships, and system administration. All REST endpoints are prefixed with `/api/v1`.

---

## Base URLs

- **Local Development**: `http://localhost:32011/api/v1`
- **Health Check**: `http://localhost:32011/healthz`

---

## Authentication

Every API call (except `/admin/init` and `/healthz`) must provide a JWT bearer token via the `Authorization` header:

```http
Authorization: Bearer <TOKEN>
```

- **Admin Tokens**: Issued via `/admin/init` or `/admin/login`, used for user registration, platform config, and system management.
- **User Tokens**: Issued via `/auth/userToken`, scoped to a specific `userID` for client actions.

---

## Endpoints

### 1. Administration & Auth

| Method | Endpoint | Description | Auth Scope |
| :--- | :--- | :--- | :--- |
| `GET` | `/healthz` | Health check endpoint | Public |
| `GET` | `/metrics` | Prometheus metrics scrape endpoint | Public |
| `POST` | `/api/v1/admin/init` | Bootstrap master admin account | Secret key required |
| `POST` | `/api/v1/admin/login` | Authenticate admin with password | Public |
| `GET` | `/api/v1/admin/health` | Admin health check | Admin |
| `POST` | `/api/v1/auth/userToken` | Mint user JWT for client sessions | Admin |
| `POST` | `/api/v1/auth/login` | User login | Public |
| `POST` | `/api/v1/auth/logout` | Invalidate an active session | User |

### 2. User Management

| Method | Endpoint | Description | Auth Scope |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/user/register` | Register a new user account | Admin |
| `GET` | `/api/v1/user/self` | Fetch current authenticated user profile | User |
| `GET` | `/api/v1/user/get?userID=...` | Fetch user profile by ID | User |

### 3. Conversations

| Method | Endpoint | Description | Auth Scope |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/conversation/create` | Create or get 1-on-1 or group conversation | User |
| `GET` | `/api/v1/conversation/list` | List active conversations for user | User |

### 4. Groups

| Method | Endpoint | Description | Auth Scope |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/group/create` | Create a new group chat | User |
| `GET` | `/api/v1/group/{groupID}` | Get group details | User |
| `GET` | `/api/v1/group/{groupID}/members` | List members of a group | User |
| `POST` | `/api/v1/group/{groupID}/invite` | Invite user into group | User |
| `POST` | `/api/v1/group/{groupID}/join` | Join a public group | User |
| `POST` | `/api/v1/group/{groupID}/kick` | Remove user from group | User |
| `POST` | `/api/v1/group/{groupID}/quit` | Leave a group | User |
| `POST` | `/api/v1/group/{groupID}/dismiss` | Dismiss / disband group | Group Owner |

### 5. Friends Graph

| Method | Endpoint | Description | Auth Scope |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/friend/request` | Send a friend request | User |
| `POST` | `/api/v1/friend/accept` | Accept an incoming friend request | User |
| `POST` | `/api/v1/friend/decline` | Decline an incoming friend request | User |
| `GET` | `/api/v1/friend/` | List confirmed friends | User |
| `GET` | `/api/v1/friend/requests/incoming` | List incoming pending requests | User |
| `GET` | `/api/v1/friend/requests/outgoing` | List outgoing pending requests | User |
| `DELETE` | `/api/v1/friend/{friendID}` | Remove an existing friend | User |

### 6. Media & Attachments

| Method | Endpoint | Description | Auth Scope |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/media/upload` | Upload media attachment (multipart) | User |
| `GET` | `/api/v1/media/` | List my uploaded attachments | User |
| `GET` | `/api/v1/media/{attachmentID}` | Get attachment metadata / pre-signed URL | User |
| `DELETE` | `/api/v1/media/{attachmentID}` | Delete attachment | User |

### 7. Search

| Method | Endpoint | Description | Auth Scope |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/v1/search/messages?q=...` | Full-text search across messages | User |

