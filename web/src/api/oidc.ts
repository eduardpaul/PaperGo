import { UserManager, WebStorageStateStore, type User } from 'oidc-client-ts';

/** The public OIDC client the server advertises in `/auth/config`. */
export interface OidcSettings {
  issuer: string;
  client_id: string;
  scope: string;
}

export const callbackPath = '/login/callback';

interface SignInState {
  returnTo: string;
}

/**
 * Browser sign-in with the authorization code flow and PKCE. The provider's
 * session and refresh tokens keep the access token current; the signed-in user
 * lives in this tab's session storage, like a pasted bearer token.
 */
export class OidcSignIn {
  private readonly manager: UserManager;

  constructor(
    readonly settings: OidcSettings,
    private readonly onToken: (accessToken: string | null) => void,
  ) {
    const origin = window.location.origin;
    this.manager = new UserManager({
      authority: settings.issuer,
      client_id: settings.client_id,
      scope: settings.scope,
      redirect_uri: origin + callbackPath,
      post_logout_redirect_uri: origin + '/login',
      response_type: 'code',
      automaticSilentRenew: true,
      loadUserInfo: false,
      monitorSession: false,
      userStore: new WebStorageStateStore({ store: window.sessionStorage }),
      stateStore: new WebStorageStateStore({ store: window.sessionStorage }),
    });
    this.manager.events.addUserLoaded((user) => this.onToken(user.access_token));
    this.manager.events.addUserUnloaded(() => this.onToken(null));
    // A failed renewal leaves the current token until it expires; the next 401
    // returns the user to the sign-in page.
    this.manager.events.addAccessTokenExpired(() => void this.renew());
  }

  /** Restores this tab's signed-in user, renewing an expired access token when possible. */
  async restore() {
    const user = await this.manager.getUser();
    if (!user) return;
    if (!user.expired) this.onToken(user.access_token);
    else await this.renew(user);
  }

  private async renew(user?: User | null) {
    user ??= await this.manager.getUser();
    if (!user?.refresh_token) {
      await this.manager.removeUser();
      return;
    }
    try {
      await this.manager.signinSilent();
    } catch {
      await this.manager.removeUser();
    }
  }

  signIn(returnTo: string) {
    return this.manager.signinRedirect({ state: { returnTo } satisfies SignInState });
  }

  /** Completes the redirect back from the provider and returns the in-app path to continue at. */
  async complete(): Promise<string> {
    const user = await this.manager.signinRedirectCallback();
    this.onToken(user.access_token);
    return safeReturnPath((user.state as SignInState | undefined)?.returnTo);
  }

  /**
   * Forgets this tab's tokens and ends the provider session. Resolves true when
   * the browser is leaving for the provider's sign-out page.
   */
  async signOut(): Promise<boolean> {
    const user = await this.manager.getUser();
    await this.manager.removeUser();
    this.onToken(null);
    if (!user) return false;
    await this.manager.signoutRedirect({ id_token_hint: user.id_token });
    return true;
  }

  forget() {
    void this.manager.removeUser();
  }
}

/** Only same-origin application paths; never another origin or a protocol-relative URL. */
export function safeReturnPath(value: unknown): string {
  return typeof value === 'string' && value.startsWith('/') && !value.startsWith('//') && !value.includes('\\')
    ? value
    : '/';
}
