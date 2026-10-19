import { Stack } from "expo-router";
import { StripeProvider } from "@stripe/stripe-react-native";
import { StatusBar } from "expo-status-bar";

const publishableKey = process.env.EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY ?? "";

export default function RootLayout() {
  return (
    <StripeProvider publishableKey={publishableKey}>
      <StatusBar style="dark" />
      <Stack screenOptions={{ headerShown: false }} />
    </StripeProvider>
  );
}
