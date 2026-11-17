import { Stack } from "expo-router";
import { StripeProvider } from "@stripe/stripe-react-native";
import { StatusBar } from "expo-status-bar";
import { ErrorBoundary } from "../src/ErrorBoundary";
import { Text, View } from "react-native";
import { mobileConfiguration } from "../src/config";

const publishableKey = process.env.EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY ?? "";

export default function RootLayout() {
  const configuration = mobileConfiguration(process.env.EXPO_PUBLIC_API_URL, __DEV__, publishableKey);
  // Validate before passing any key to the native SDK. A bad release must show
  // a recoverable configuration screen, not crash during provider startup.
  if (configuration.error) return <View style={{ padding: 32, paddingTop: 72 }}><Text accessibilityRole="header">Configuration required</Text><Text accessibilityRole="alert">{configuration.error}</Text></View>;
  return (
    <ErrorBoundary><StripeProvider publishableKey={publishableKey}>
      <StatusBar style="dark" />
      <Stack screenOptions={{ headerShown: false }} />
    </StripeProvider></ErrorBoundary>
  );
}
