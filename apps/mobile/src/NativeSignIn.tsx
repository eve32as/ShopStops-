import * as AuthSession from "expo-auth-session";
import { useEffect, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";

const issuer = process.env.EXPO_PUBLIC_OIDC_ISSUER?.trim() ?? "";
const clientId = process.env.EXPO_PUBLIC_OIDC_CLIENT_ID?.trim() ?? "";
const fallbackDiscovery: AuthSession.DiscoveryDocument = {
  authorizationEndpoint: "",
  tokenEndpoint: "",
};

function isSecureUrl(value: string) {
  try {
    const url = new URL(value);
    return url.protocol === "https:" && Boolean(url.host) && !url.username && !url.password &&
      !url.search && !url.hash;
  } catch {
    return false;
  }
}

function isSecureDiscovery(discovery: AuthSession.DiscoveryDocument | null) {
  return Boolean(
    discovery &&
      discovery.authorizationEndpoint &&
      discovery.tokenEndpoint &&
      isSecureUrl(discovery.authorizationEndpoint) &&
      isSecureUrl(discovery.tokenEndpoint),
  );
}

type NativeSignInProps = {
  signedIn: boolean;
  disabled: boolean;
  gatewayUrl: string;
  onSignedIn: (accessToken: string, expiresAt: number) => Promise<void>;
  onSignOut: () => Promise<void>;
};

function isAllowedGatewayUrl(value: string) {
  try {
    const url = new URL(value.trim());
    const localDevelopment = url.protocol === "http:" &&
      ["localhost", "127.0.0.1", "10.0.2.2"].includes(url.hostname);
    return !url.username && !url.password && !url.search && !url.hash &&
      (url.protocol === "https:" || localDevelopment);
  } catch {
    return false;
  }
}

export function NativeSignIn({
  signedIn,
  disabled,
  gatewayUrl,
  onSignedIn,
  onSignOut,
}: NativeSignInProps) {
  const configuredIssuer = isSecureUrl(issuer);
  const gatewayReady = isAllowedGatewayUrl(gatewayUrl);
  const [discovery, setDiscovery] = useState<AuthSession.DiscoveryDocument | null>(
    configuredIssuer ? null : fallbackDiscovery,
  );
  const [discoveryError, setDiscoveryError] = useState(false);
  useEffect(() => {
    let active = true;
    if (!configuredIssuer) {
      setDiscovery(fallbackDiscovery);
      setDiscoveryError(false);
      return () => {
        active = false;
      };
    }

    setDiscovery(null);
    setDiscoveryError(false);
    const discoveryUrl = `${issuer.replace(/\/+$/, "")}/.well-known/openid-configuration`;
    void fetch(discoveryUrl, { headers: { Accept: "application/json" } })
      .then(async (response) => {
        if (
          !response.ok ||
          !isSecureUrl(response.url) ||
          new URL(response.url).origin !== new URL(discoveryUrl).origin
        ) {
          throw new Error("OIDC discovery request failed");
        }
        const metadata: unknown = await response.json();
        if (
          !metadata ||
          typeof metadata !== "object" ||
          !("issuer" in metadata) ||
          metadata.issuer !== issuer ||
          !("authorization_endpoint" in metadata) ||
          typeof metadata.authorization_endpoint !== "string" ||
          !("token_endpoint" in metadata) ||
          typeof metadata.token_endpoint !== "string" ||
          !isSecureUrl(metadata.authorization_endpoint) ||
          !isSecureUrl(metadata.token_endpoint)
        ) {
          throw new Error("OIDC discovery metadata is invalid");
        }
        if (active) {
          setDiscovery({
            authorizationEndpoint: metadata.authorization_endpoint,
            tokenEndpoint: metadata.token_endpoint,
          });
        }
      })
      .catch(() => {
        if (active) setDiscoveryError(true);
      });

    return () => {
      active = false;
    };
  }, [configuredIssuer]);
  const redirectUri = AuthSession.makeRedirectUri({ scheme: "shopstops", path: "auth" });
  const [request, , promptAsync] = AuthSession.useAuthRequest({
    clientId,
    redirectUri,
    responseType: AuthSession.ResponseType.Code,
    scopes: ["openid", "profile", "email"],
    usePKCE: true,
  }, discovery);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function signIn() {
    if (!request || !discovery || !isSecureDiscovery(discovery) || !clientId) {
      setError("Sign-in is not configured. Check the OIDC issuer and public client ID.");
      return;
    }

    setBusy(true);
    setError("");
    try {
      const result = await promptAsync();
      if (result.type !== "success") {
        if (result.type === "error") setError("The identity provider could not complete sign-in.");
        return;
      }
      const code = result.params.code;
      if (!code || !request.codeVerifier) {
        setError("The identity provider returned an incomplete sign-in response.");
        return;
      }
      const tokenResponse = await AuthSession.exchangeCodeAsync({
        clientId,
        code,
        redirectUri,
        extraParams: { code_verifier: request.codeVerifier },
      }, discovery);
      if (!tokenResponse.accessToken) {
        setError("The identity provider did not return an access token.");
        return;
      }
      const expiresAt = tokenResponse.expiresIn
        ? Date.now() + tokenResponse.expiresIn * 1000
        : 0;
      await onSignedIn(tokenResponse.accessToken, expiresAt);
    } catch {
      setError("Sign-in could not be completed. Please try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <View style={styles.container}>
      <Text style={styles.title}>Sign in securely</Text>
      <Text style={styles.description}>
        ShopStops opens your organization’s identity provider. Your password is never entered into
        ShopStops.
      </Text>
      {signedIn ? (
        <Pressable
          accessibilityRole="button"
          disabled={disabled || busy}
          onPress={() => {
            setError("");
            void onSignOut();
          }}
          style={[styles.button, styles.signOutButton, (disabled || busy) && styles.disabled]}
        >
          <Text style={styles.buttonText}>Sign out of ShopStops</Text>
        </Pressable>
      ) : (
        <Pressable
          accessibilityRole="button"
          disabled={disabled || busy || !gatewayReady || !request || !isSecureDiscovery(discovery)}
          onPress={() => void signIn()}
          style={[styles.button, (disabled || busy || !gatewayReady || !request || !isSecureDiscovery(discovery)) && styles.disabled]}
        >
          {busy ? <ActivityIndicator color="#ffffff" /> : <Text style={styles.buttonText}>Continue with your organization</Text>}
        </Pressable>
      )}
      {!configuredIssuer || !clientId ? (
        <Text style={styles.help}>Configure EXPO_PUBLIC_OIDC_ISSUER and EXPO_PUBLIC_OIDC_CLIENT_ID to enable sign-in.</Text>
      ) : null}
      {!gatewayReady ? (
        <Text style={styles.help}>Enter an HTTPS gateway URL before signing in.</Text>
      ) : null}
      {discoveryError ? (
        <Text style={styles.error}>Could not securely load the identity provider configuration.</Text>
      ) : null}
      {error ? <Text accessibilityLiveRegion="polite" style={styles.error}>{error}</Text> : null}
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    gap: 10,
    marginTop: 8,
  },
  title: {
    color: "#18352f",
    fontSize: 17,
    fontWeight: "700",
  },
  description: {
    color: "#64748b",
    fontSize: 13,
    lineHeight: 19,
  },
  button: {
    alignItems: "center",
    backgroundColor: "#19352f",
    borderRadius: 12,
    justifyContent: "center",
    minHeight: 48,
    paddingHorizontal: 14,
  },
  signOutButton: {
    backgroundColor: "#52625f",
  },
  disabled: {
    opacity: 0.5,
  },
  buttonText: {
    color: "#ffffff",
    fontSize: 14,
    fontWeight: "700",
  },
  help: {
    color: "#8a5a16",
    fontSize: 12,
    lineHeight: 17,
  },
  error: {
    color: "#b42318",
    fontSize: 13,
  },
});
