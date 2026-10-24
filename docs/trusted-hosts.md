# Trusted hosts

The organization keeps one list of **trusted hosts**: the outside hosts PaperGo fetches from and shows without asking. Every feature that reaches outside PaperGo follows it, so changing the list changes them all at once.

| Feature | Host on the list | Host not on the list |
| --- | --- | --- |
| Images in Markdown | Load with the document | A placeholder naming the host, with **Show once** and **Show all images in this file**; administrators also get **Trust host** |
| Links in Markdown, PDFs and URL fields | Open in a new tab without a referrer | A confirmation, "You're leaving PaperGo for *host*", showing the whole address before it opens |
| [List integrations](list-integrations.md) | Refresh | Refused when added or edited ("*host* isn't a trusted host. Ask an administrator to trust it."); an existing integration stops refreshing and shows **Host no longer trusted**, keeps its items, and resumes once the host is trusted again |

Links and images inside PaperGo itself are always trusted. Images embedded in a file are part of the file and always show.

## Entries

An entry is one of:

- a host name, such as `docs.example.com`;
- a wildcard for the subdomains of a host, such as `*.example.com`, which does not match `example.com` itself;
- `*`, which trusts every public host.

Entries are stored lowercase, without a trailing dot, with international names in their ASCII (punycode) form. IP addresses, ports, schemes and paths are refused. Only `*` trusts a host named by an IP address, and only for server-side fetches, which reach public addresses alone; in the browser, links to an IP address always ask first and images from one wait for **Show once**. The list holds at most 200 entries.

A new organization trusts `*`, so everything works at first. To narrow it, add the hosts the organization uses, then remove `*`. While `*` is listed, nothing offers **Trust host**.

The list never opens private networks: server-side fetches still reach only public addresses over HTTPS, whatever it says. It does not cover what the operator configures, such as AI endpoints, OIDC, the MCP URL or CORS.

## Managing the list

**Administration → Trusted hosts** shows the entries and how many integrations use each. Removing an entry that integrations still need names them and asks first. Administrators can also trust a host from where it was needed: a link's confirmation, an image placeholder, or a refused integration.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/v1/organization/settings/trusted-hosts` | `hosts` and `version` (`0` until first changed); any active account |
| PUT | `/v1/organization/settings/trusted-hosts` | Replace `hosts`, with the current version in `If-Match`; `organization.manage` |
| GET | `/v1/organization/settings/trusted-hosts/usage` | The hosts integrations fetch from, each with its count of `integrations`; `organization.manage` |

Changes are audited as `settings.trusted_hosts`. Open browsers read the list again when their window regains focus.
