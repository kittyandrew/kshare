# Zitadel data model for kshare

OIDC org/user/project/role/apps that `kshare-cli` and `kshared` server
authenticate against. All steps as IAM_OWNER (`zitadel-admin`) in
your Zitadel Console (replace `https://auth.example.com` throughout
with your issuer). Order matters -- dependencies are hard.

## Choose your org layout

Before step 1, pick one:

**A. Reuse an existing org.** If you're already running Zitadel for
another service, your existing user + passkey carry over; you only
create the `kshare` project + role + apps inside that org. **Skip
steps 1, 2, 3.** Step 6 is a same-org grant.

**B. Fresh `kshare` org.** Hard isolation between services. You'll
either create a new user (do steps 1-3) or grant an existing user
from another org access to the kshare project (skip step 3, use step
6's cross-org grant note).

For a single-uploader personal share, **A** is simpler. **B** is
right if you want admin-UI separation between services or anticipate
multi-tenant boundaries later. Either way, the resulting `.env` is
the same shape; only what you click in the Console differs.

## 1 -- Org `kshare` (option B only)

Top-bar org switcher -> **Create New Organization** -> name `kshare`.
Record the numeric **org ID**. Username domain becomes
`<localpart>@kshare.<ExternalDomain>`.

## 2 -- Login policy (option B only)

`kshare` org scope -> Settings -> **Login Behavior and Security**:

- Local authentication allowed: ENABLED (required; v2 loginname page
  gates the username input on this).
- User Registration: DISABLED.
- External Login: DISABLED.
- Passkey Login dropdown: **Allowed**; Force MFA: DISABLED (passkey
  alone is sufficient).

After saves: restart the Zitadel login service on the host (15-min
login UI cache).

## 3 -- User (option B only, fresh-user variant)

Skip if you're granting access to an existing user from another org
(see step 6's cross-org note). Otherwise:

Org -> Users -> **+ New** -> Human user. Username (suffixed with org
domain), email, "Email verified" CHECKED. **Skip the password section.**

After save: user detail -> Authenticators -> **Send passkey
registration link** (requires SMTP configured on your Zitadel host).
Open the email in a browser and enroll a passkey. Use Solokey direct
or platform authenticator (TouchID / Windows Hello); avoid
Bitwarden's passkey impl against Zitadel `userVerification=required`.

## 4 -- Project

Org -> Projects -> **+ New** -> name `kshare`. Record the numeric
**project ID** (needed for the audience pin in `KSHARE_OIDC_AUDIENCE`
and the per-project role claim URN).

Project detail page -> Project Settings card:

- Return user roles during authentication (`projectRoleAssertion`):
  ENABLED. Role claims flow into tokens.
- Only authorized users can authenticate (`projectRoleCheck`):
  ENABLED. Denies login if the user has no role on this project.
- Authentication is restricted to users from organizations...
  (`hasProjectCheck`): DISABLED (single-org grant; not needed).

## 5 -- Role

Project -> Roles tab -> **+ New**:

- Key: `upload`
- Display: `Upload to kshare`
- Group: `kshare`

Save. One role only.

## 6 -- Grant role to user

**Same-org user (option A, or option B + fresh user from step 3):**

Project -> **Role Assignments** sidebar -> **+ New** -> select user
-> Continue -> check `upload` -> Save.

**Cross-org user (option B reusing a user from another org):**

Project -> **User Grants** sidebar (NOT "Role Assignments" — that's
same-org only) -> **+ New** -> search by username or email
(`<localpart>@<other-org>.<external-domain>`). As IAM_OWNER you can
grant across orgs. Select the `upload` role -> Save.

Verification: the user can log in via device flow, and decoded
access tokens include
`urn:zitadel:iam:org:project:<kshare-project-id>:roles` containing
`upload` — regardless of which org the user lives in.

## 7 -- API app `kshare-server` (resource server / audience)

Project -> General -> **+ New Application** -> 3-step wizard:

- Step 1: Name `kshare-server`, Type **API**, Continue
- Step 2: Auth Method **JWT (Private Key)**, Continue
- Step 3: Create

Wizard offers a `key.json` download. **There is no re-download path
in the v4 console**, so download it now even if you don't strictly
need it for the resource-server role (the server validates JWTs
against the issuer's JWKS, not its own private key -- the API app
exists just to define the audience). Stash for safekeeping at
`~/.config/kshare/zitadel-server-key.json` (mode 0600).

Record the **Client ID** from the app detail page header. The
audience the server validates is the **project ID** (step 4), not
this client ID.

## 8 -- Native app `kshare-cli` (device flow)

Project -> General -> **+ New Application** -> 3-step wizard:

- Step 1: Name `kshare-cli`, Type **Native** (under OIDC), Continue
- Step 2: Auth Method **Device Code**, Continue
- Step 3: Create

After creation, app detail -> **Token Settings** sidebar ->
**AuthToken Options** card:

- Auth Token Type dropdown: **JWT** (changes from the default Bearer
  Token). This must be JWT -- the server verifies the token offline
  against the JWKS; opaque bearer tokens require introspection RTT
  to Zitadel per request, which we don't want.
- Add user roles to the access token: ENABLED. **Critical** -- the
  middleware reads
  `urn:zitadel:iam:org:project:<projectID>:roles` from the access
  token. Without this toggle the role check always denies.
- User roles inside ID Token: ENABLED (harmless, useful for debug)
- Include user's profile info in ID Token: ENABLED (harmless)
- ClockSkew: 0

Save. Record the **Client ID** -- this is what
`KSHARE_OIDC_CLIENT_ID` points at.

## 9 -- End-to-end verification

```bash
ISSUER=https://auth.example.com
CID=<kshare-cli client id>
PID=<kshare project id>

# 1. Start device flow
curl -sX POST $ISSUER/oauth/v2/device_authorization \
  -d "client_id=$CID" \
  -d "scope=openid offline_access urn:zitadel:iam:org:project:id:$PID:aud urn:zitadel:iam:org:projects:roles" \
  | jq

# 2. Rewrite the verification_uri_complete path: /device -> /ui/v2/login/device
#    (see pitfalls below). Open in incognito. Type the user_code.
#    Sign in with passkey. Consent.

# 3. Exchange device_code for tokens
curl -sX POST $ISSUER/oauth/v2/token \
  -d grant_type=urn:ietf:params:oauth:grant-type:device_code \
  -d "device_code=<from step 1>" \
  -d "client_id=$CID" \
  | jq

# 4. Decode the access token payload and verify:
#    .iss == $ISSUER
#    .aud contains $PID
#    ."urn:zitadel:iam:org:project:<$PID>:roles" contains "upload"
echo "<access_token>" | cut -d. -f2 | base64 -d 2>/dev/null | jq
```

If step 4 doesn't show the `upload` role under the project-scoped
URN, the "Add user roles to the access token" toggle in step 8 is
the most likely culprit (and is the canonical Zitadel silent failure).

## Common pitfalls

- **Empty loginname form.** `allowLocalAuthentication=false` somewhere
  in the policy chain (org -> instance fallback). Check all three
  scopes (org, default org, instance) and restart the login
  container (`systemctl restart docker-zitadel-login.service`; 15-min
  cache).
- **"Send passkey registration link" silently fails.** SMTP is not
  configured on your Zitadel instance.
- **Device verification URI routes to legacy v1 login**
  (`/device?...`) which can't see v2-projected passkeys for
  org-scoped users. The CLI rewrites the path to
  `/ui/v2/login/device` before opening the browser
  (`cmd/cli/auth.go::runLogin`).
- **Forgot the "Add user roles to access token" toggle.** Step 8.
  Without it, role check denies and the user sees
  `forbidden: missing role upload`.
- **Audience mismatch.** `KSHARE_OIDC_AUDIENCE` must be the
  numeric **project ID** (step 4), not the API app's Client ID
  (step 7). Easy mistake.
- **Cross-org grant in wrong sidebar.** "Role Assignments" only
  shows users from the project's own org. To grant a user from a
  different org, use "User Grants" instead (step 6).

## What lives where

| Value | Where |
|---|---|
| `KSHARE_OIDC_ISSUER` | constant: `https://auth.example.com` |
| `KSHARE_OIDC_AUDIENCE` | project ID from step 4 |
| `KSHARE_OIDC_CLIENT_ID` | `kshare-cli` Client ID from step 8 |
| `kshare-server` Client ID | not used by the server at runtime; exists only to declare the audience |
| `~/.config/kshare/zitadel-server-key.json` | downloaded in step 7; archive for safekeeping. Not consumed by `kshared` (resource server, not client) |
