import * as Location from "expo-location";
import { useStripe } from "@stripe/stripe-react-native";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Image,
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { ApiError, apiRequest, idempotencyKey, type ApiSettings } from "../src/api";

type Role = "Shopper" | "Driver" | "Merchant";
type NearbyMerchant = {
  merchantId: string;
  name: string;
  merchantType: string;
  storeLocationId: string;
  distanceMeters: number;
};
type CatalogItem = {
  id: string;
  name: string;
  description: string;
  unitPriceCents: number;
  volumeLiters: string;
};
type MerchantCatalogItem = CatalogItem & {
  storeLocationId: string;
  available: boolean;
};
type MerchantStoreLocation = {
  id: string;
  name: string;
};
type MerchantCatalogDraft = {
  name: string;
  description: string;
  unitPriceCents: string;
  volumeLiters: string;
};
type CreateOrderBody = {
  storeLocationId: string;
  dropoff: { latitude: number; longitude: number };
  deliveryWindowStart: string;
  deliveryWindowEnd: string;
  items: { catalogItemId: string; quantity: number }[];
};
type DeliveryWindow = {
  start: string;
  end: string;
};
type Checkout = {
  id: string;
  status: string;
  currency: string;
  totalCents: number;
  invoiceLines: { type: string; merchantId?: string; amountCents: number }[];
};
type Route = {
  id: string;
  status: string;
  routingProvider: string;
  stops: {
    id: string;
    sequence: number;
    type: string;
    latitude: number;
    longitude: number;
    status: string;
  }[];
};
type StopEvent = {
  stopId: string;
  stopStatus: string;
  routeStatus: string;
};
type Subscription = {
  status: string;
  monthlyAmountCents: number;
  currency: string;
  storefrontVisible: boolean;
  currentPeriodEnd: string | null;
  paymentMethodReady: boolean;
};

const stripeKeyConfigured = Boolean(process.env.EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY);

function createDeliveryWindow(): DeliveryWindow {
  const start = new Date(Date.now() + 15 * 60 * 1000);
  return {
    start: start.toISOString(),
    end: new Date(start.getTime() + 2 * 60 * 60 * 1000).toISOString(),
  };
}

export default function HomeScreen() {
  const [role, setRole] = useState<Role>("Shopper");
  const [connectionOpen, setConnectionOpen] = useState(true);
  const [gatewayUrl, setGatewayUrl] = useState("");
  const [sessionToken, setSessionToken] = useState("");
  const settings = useMemo<ApiSettings>(
    () => ({ baseUrl: gatewayUrl, sessionToken }),
    [gatewayUrl, sessionToken],
  );
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [nearby, setNearby] = useState<NearbyMerchant[]>([]);
  const [catalog, setCatalog] = useState<CatalogItem[]>([]);
  const [catalogStoreID, setCatalogStoreID] = useState("");
  const [quantities, setQuantities] = useState<Record<string, number>>({});
  const [checkout, setCheckout] = useState<Checkout | null>(null);
  const [route, setRoute] = useState<Route | null>(null);
  const [subscription, setSubscription] = useState<Subscription | null>(null);
  const [merchantStoreLocations, setMerchantStoreLocations] = useState<MerchantStoreLocation[]>([]);
  const [merchantCatalog, setMerchantCatalog] = useState<MerchantCatalogItem[]>([]);
  const [merchantCatalogDrafts, setMerchantCatalogDrafts] = useState<Record<string, MerchantCatalogDraft>>({});
  const [merchantStoreLocation, setMerchantStoreLocation] = useState("");
  const [newItemName, setNewItemName] = useState("");
  const [newItemDescription, setNewItemDescription] = useState("");
  const [newItemPrice, setNewItemPrice] = useState("");
  const [newItemVolume, setNewItemVolume] = useState("");
  const [anchorStore, setAnchorStore] = useState("");
  const [orderIds, setOrderIds] = useState("");
  const [latitude, setLatitude] = useState("");
  const [longitude, setLongitude] = useState("");
  const [routeId, setRouteId] = useState("");
  const [tracking, setTracking] = useState(false);
  const [deliveryWindow, setDeliveryWindow] = useState<DeliveryWindow>(() => createDeliveryWindow());
  const basketWindow = useRef<DeliveryWindow | null>(null);
  const orderAttempt = useRef<{ key: string; fingerprint: string; body: CreateOrderBody } | null>(null);
  const merchantCatalogAttempt = useRef<{
    key: string;
    fingerprint: string;
    body: {
      storeLocationId: string;
      name: string;
      description: string;
      unitPriceCents: number;
      volumeLiters: number;
    };
  } | null>(null);
  const checkoutAttempt = useRef<{
    key: string;
    body: {
      anchorStoreLocationId: string;
      orderIds: string[];
      dropoff: { latitude: number; longitude: number };
      deliveryWindowStart: string;
      deliveryWindowEnd: string;
    };
  } | null>(null);
  const routeLocation = useRef<Location.LocationObject | null>(null);
  const routeSubscription = useRef<Location.LocationSubscription | null>(null);
  const lastReportedAt = useRef(0);
  const previousConnection = useRef({ gatewayUrl, sessionToken });
  const { initPaymentSheet, presentPaymentSheet } = useStripe();

  const run = useCallback(async <T,>(action: () => Promise<T>) => {
    setBusy(true);
    setMessage("");
    try {
      return await action();
    } catch (error) {
      setMessage(error instanceof ApiError ? error.message : "The request could not be completed.");
      return undefined;
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    return () => {
      routeSubscription.current?.remove();
      routeSubscription.current = null;
    };
  }, []);

  useEffect(() => {
    if (
      previousConnection.current.gatewayUrl !== gatewayUrl ||
      previousConnection.current.sessionToken !== sessionToken
    ) {
      previousConnection.current = { gatewayUrl, sessionToken };
      endTracking();
      checkoutAttempt.current = null;
      orderAttempt.current = null;
      basketWindow.current = null;
      setDeliveryWindow(createDeliveryWindow());
      setNearby([]);
      setCatalog([]);
      setCatalogStoreID("");
      setQuantities({});
      setOrderIds("");
      setCheckout(null);
      setRoute(null);
      setSubscription(null);
      setMerchantCatalog([]);
      setMerchantCatalogDrafts({});
      setMerchantStoreLocations([]);
      setMerchantStoreLocation("");
    }
  }, [gatewayUrl, sessionToken]);

  useEffect(() => {
    if (!tracking) return;
    const timer = setInterval(() => {
      const current = routeLocation.current;
      if (!current || !routeId || current.timestamp <= lastReportedAt.current) return;
      lastReportedAt.current = current.timestamp;
      void apiRequest(
        settings,
        `driver/routes/${encodeURIComponent(routeId)}/location`,
        {
          method: "POST",
          body: JSON.stringify({
            eventId: `gps-${Date.now()}-${Math.random().toString(36).slice(2)}`,
            observedAt: new Date(current.timestamp).toISOString(),
            location: {
              latitude: current.coords.latitude,
              longitude: current.coords.longitude,
            },
          }),
        },
      ).catch((error: unknown) => {
        setMessage(error instanceof Error ? error.message : "GPS update failed.");
      });
    }, 5000);
    return () => clearInterval(timer);
  }, [routeId, settings, tracking]);

  async function findNearby() {
    const results = await run(() =>
      apiRequest<NearbyMerchant[]>(
        settings,
        `stores/${encodeURIComponent(anchorStore)}/nearby-merchants?radiusMeters=500`,
      ),
    );
    if (results) setNearby(results);
  }

  async function loadCatalog(storeID = catalogStoreID) {
    const normalizedID = storeID.trim();
    if (!normalizedID) {
      setMessage("Enter a store location ID to browse its catalog.");
      return;
    }
    const results = await run(() =>
      apiRequest<CatalogItem[]>(
        settings,
        `stores/${encodeURIComponent(normalizedID)}/catalog`,
      ),
    );
    if (results) {
      setCatalogStoreID(normalizedID);
      setCatalog(results);
      setQuantities({});
    }
  }

  async function createCustomerOrder() {
    if (checkout) {
      setMessage("Start a new basket before creating another order.");
      return;
    }
    const lat = Number(latitude);
    const lon = Number(longitude);
    const items = catalog
      .filter((item) => (quantities[item.id] ?? 0) > 0)
      .map((item) => ({ catalogItemId: item.id, quantity: quantities[item.id] }));
    if (
      !catalogStoreID.trim() || !Number.isFinite(lat) || lat < -90 || lat > 90 ||
      !Number.isFinite(lon) || lon < -180 || lon > 180 || items.length === 0
    ) {
      setMessage("Choose catalog items and enter valid delivery coordinates.");
      return;
    }
    const window = basketWindow.current ?? createDeliveryWindow();
    basketWindow.current = window;
    setDeliveryWindow(window);
    const body: CreateOrderBody = {
      storeLocationId: catalogStoreID.trim(),
      dropoff: { latitude: lat, longitude: lon },
      deliveryWindowStart: window.start,
      deliveryWindowEnd: window.end,
      items,
    };
    const fingerprint = JSON.stringify(body);
    if (orderAttempt.current?.fingerprint !== fingerprint) {
      orderAttempt.current = { key: idempotencyKey(), fingerprint, body };
    }
    const attempt = orderAttempt.current;
    const result = await run(() =>
      apiRequest<{ id: string; status: string; itemSubtotalCents: number }>(
        settings,
        "orders",
        {
          method: "POST",
          headers: { "Idempotency-Key": attempt.key },
          body: JSON.stringify(attempt.body),
        },
      ),
    );
    if (result) {
      orderAttempt.current = null;
      checkoutAttempt.current = null;
      const currentIDs = orderIds.split(/[\s,]+/).map((id) => id.trim()).filter(Boolean);
      if (!currentIDs.includes(result.id)) {
        setOrderIds([...currentIDs, result.id].join(", "));
      }
      setQuantities({});
      setMessage(`Order created · $${(result.itemSubtotalCents / 100).toFixed(2)} items.`);
    }
  }

  async function createCheckout() {
    const ids = orderIds
      .split(/[\s,]+/)
      .map((id) => id.trim())
      .filter(Boolean);
    const lat = Number(latitude);
    const lon = Number(longitude);
    if (!anchorStore.trim() || ids.length === 0 || !Number.isFinite(lat) || !Number.isFinite(lon)) {
      setMessage("Enter the anchor store, order IDs, and delivery coordinates.");
      return;
    }
    if (!checkoutAttempt.current) {
      const window = basketWindow.current ?? createDeliveryWindow();
      basketWindow.current = window;
      setDeliveryWindow(window);
      checkoutAttempt.current = {
        key: idempotencyKey(),
        body: {
          anchorStoreLocationId: anchorStore.trim(),
          orderIds: ids,
          dropoff: { latitude: lat, longitude: lon },
          deliveryWindowStart: window.start,
          deliveryWindowEnd: window.end,
        },
      };
    }
    const attempt = checkoutAttempt.current;
    const result = await run(() =>
      apiRequest<Checkout>(settings, "checkouts", {
        method: "POST",
        headers: { "Idempotency-Key": attempt.key },
        body: JSON.stringify(attempt.body),
      }),
    );
    if (result) {
      checkoutAttempt.current = null;
      setCheckout(result);
    }
  }

  function startNewBasket() {
    orderAttempt.current = null;
    checkoutAttempt.current = null;
    basketWindow.current = null;
    setDeliveryWindow(createDeliveryWindow());
    setOrderIds("");
    setCheckout(null);
    setQuantities({});
    setMessage("Started a new basket.");
  }

  async function payCheckout() {
    if (!checkout || !stripeKeyConfigured) return;
    const intent = await run(() =>
      apiRequest<{ clientSecret: string; amountCents: number; currency: string }>(
        settings,
        `checkouts/${encodeURIComponent(checkout.id)}/payment`,
        { method: "POST" },
      ),
    );
    if (!intent) return;
    const initialized = await initPaymentSheet({
      merchantDisplayName: "ShopStops",
      paymentIntentClientSecret: intent.clientSecret,
    });
    if (initialized.error) {
      setMessage(initialized.error.message);
      return;
    }
    const presented = await presentPaymentSheet();
    if (presented.error) setMessage(presented.error.message);
    else setMessage("Payment submitted. Your order updates when Stripe confirms it.");
  }

  async function loadRoute() {
    if (!routeId.trim()) {
      setMessage("Enter your assigned route ID.");
      return;
    }
    const result = await run(() =>
      apiRequest<Route>(settings, `driver/routes/${encodeURIComponent(routeId.trim())}`),
    );
    if (result) setRoute(result);
  }

  async function updateRouteStop(stopId: string, type: "arrive" | "complete") {
    const routeKey = routeId.trim();
    const result = await run(() =>
      apiRequest<StopEvent>(
        settings,
        `driver/routes/${encodeURIComponent(routeKey)}/stops/${encodeURIComponent(stopId)}/events`,
        {
          method: "POST",
          body: JSON.stringify({
            eventId: idempotencyKey(),
            type,
            observedAt: new Date().toISOString(),
          }),
        },
      ),
    );
    if (result) {
      setRoute((current) =>
        current
          ? {
              ...current,
              status: result.routeStatus,
              stops: current.stops.map((stop) =>
                stop.id === result.stopId ? { ...stop, status: result.stopStatus } : stop,
              ),
            }
          : current,
      );
    }
  }

  async function beginTracking() {
    if (!routeId.trim()) {
      setMessage("Load your assigned route before starting location sharing.");
      return;
    }
    const permission = await Location.requestForegroundPermissionsAsync();
    if (permission.status !== "granted") {
      setMessage("Allow location access while using the app to share your active route.");
      return;
    }
    try {
      routeLocation.current = await Location.getCurrentPositionAsync({
        accuracy: Location.Accuracy.Balanced,
      });
      routeSubscription.current = await Location.watchPositionAsync(
        {
          accuracy: Location.Accuracy.Balanced,
          timeInterval: 5000,
          distanceInterval: 0,
        },
        (position) => {
          routeLocation.current = position;
        },
      );
      setTracking(true);
      setMessage("Location sharing is active while this screen remains open.");
    } catch {
      setMessage("Could not start location updates. Check device location services.");
    }
  }

  function endTracking() {
    routeSubscription.current?.remove();
    routeSubscription.current = null;
    routeLocation.current = null;
    lastReportedAt.current = 0;
    setTracking(false);
    setMessage("Location sharing stopped.");
  }

  useEffect(() => {
    if ((role !== "Driver" || !settings.sessionToken.trim()) && tracking) endTracking();
  }, [role, settings.sessionToken, tracking]);

  async function refreshSubscription() {
    const result = await run(() => apiRequest<Subscription>(settings, "merchant/subscription"));
    if (result) setSubscription(result);
  }

  async function setupMerchantPaymentMethod() {
    const intent = await run(() =>
      apiRequest<{ clientSecret: string }>(settings, "merchant/subscription/setup-intent", {
        method: "POST",
        headers: { "Idempotency-Key": idempotencyKey() },
      }),
    );
    if (!intent) return;
    const initialized = await initPaymentSheet({
      merchantDisplayName: "ShopStops Merchant",
      setupIntentClientSecret: intent.clientSecret,
    });
    if (initialized.error) {
      setMessage(initialized.error.message);
      return;
    }
    const presented = await presentPaymentSheet();
    if (presented.error) {
      setMessage(presented.error.message);
      return;
    }
    setMessage("Billing method saved. Refresh status before starting your subscription.");
    await refreshSubscription();
  }

  async function startSubscription() {
    const result = await run(() =>
      apiRequest<{ status: string }>(settings, "merchant/subscription", { method: "POST" }),
    );
    if (result) {
      setMessage(`Subscription is ${result.status}. Storefront activation waits for a paid invoice.`);
      await refreshSubscription();
    }
  }

  async function loadMerchantCatalog() {
    const items = await run(() =>
      apiRequest<MerchantCatalogItem[]>(settings, "merchant/catalog"),
    );
    if (items) {
      setMerchantCatalog(items);
      setMerchantCatalogDrafts(Object.fromEntries(items.map((item) => [
        item.id,
        {
          name: item.name,
          description: item.description,
          unitPriceCents: String(item.unitPriceCents),
          volumeLiters: item.volumeLiters,
        },
      ])));
    }
  }

  async function loadMerchantStoreLocations() {
    const locations = await run(() =>
      apiRequest<MerchantStoreLocation[]>(settings, "merchant/store-locations"),
    );
    if (locations) {
      setMerchantStoreLocations(locations);
      if (!merchantStoreLocation && locations.length > 0) {
        setMerchantStoreLocation(locations[0].id);
      }
    }
  }

  async function createMerchantCatalogItem() {
    const price = Number(newItemPrice);
    const volume = Number(newItemVolume);
    if (!merchantStoreLocation.trim() || !newItemName.trim() ||
        !Number.isInteger(price) || price < 1 || !Number.isFinite(volume) || volume <= 0) {
      setMessage("Enter your store location ID, item name, price in cents, and positive volume.");
      return;
    }
    const body = {
      storeLocationId: merchantStoreLocation.trim(),
      name: newItemName.trim(),
      description: newItemDescription,
      unitPriceCents: price,
      volumeLiters: volume,
    };
    const fingerprint = JSON.stringify(body);
    if (merchantCatalogAttempt.current?.fingerprint !== fingerprint) {
      merchantCatalogAttempt.current = { key: idempotencyKey(), fingerprint, body };
    }
    const attempt = merchantCatalogAttempt.current;
    const item = await run(() =>
      apiRequest<MerchantCatalogItem>(settings, "merchant/catalog", {
        method: "POST",
        headers: { "Idempotency-Key": attempt.key },
        body: JSON.stringify(attempt.body),
      }),
    );
    if (item) {
      merchantCatalogAttempt.current = null;
      setNewItemName("");
      setNewItemDescription("");
      setNewItemPrice("");
      setNewItemVolume("");
      setMessage("Catalog item saved.");
      await loadMerchantCatalog();
    }
  }

  async function updateMerchantCatalogItem(item: MerchantCatalogItem) {
    const draft = merchantCatalogDrafts[item.id];
    if (!draft) return;
    const price = Number(draft.unitPriceCents);
    const volume = Number(draft.volumeLiters);
    if (!draft.name.trim() || !Number.isInteger(price) || price < 1 ||
        !Number.isFinite(volume) || volume <= 0) {
      setMessage("Enter a valid item name, price in cents, and positive volume.");
      return;
    }
    const updated = await run(() =>
      apiRequest<MerchantCatalogItem>(
        settings,
        `merchant/catalog/${encodeURIComponent(item.id)}`,
        {
          method: "PATCH",
          body: JSON.stringify({
            ...draft,
            name: draft.name.trim(),
            unitPriceCents: price,
            volumeLiters: volume,
            available: item.available,
          }),
        },
      ),
    );
    if (updated) {
      setMerchantCatalog((current) => current.map((entry) =>
        entry.id === updated.id ? updated : entry,
      ));
      setMessage("Catalog item updated.");
    }
  }

  async function toggleMerchantCatalogAvailability(item: MerchantCatalogItem) {
    const draft = merchantCatalogDrafts[item.id];
    if (!draft) return;
    const updated = await run(() =>
      apiRequest<MerchantCatalogItem>(
        settings,
        `merchant/catalog/${encodeURIComponent(item.id)}`,
        {
          method: "PATCH",
          body: JSON.stringify({
            ...draft,
            unitPriceCents: Number(draft.unitPriceCents),
            volumeLiters: Number(draft.volumeLiters),
            available: !item.available,
          }),
        },
      ),
    );
    if (updated) {
      setMerchantCatalog((current) => current.map((entry) =>
        entry.id === updated.id ? updated : entry,
      ));
    }
  }

  const sharedForm = (
    <View style={styles.configCard}>
      <Text style={styles.sectionEyebrow}>SECURE CONNECTION</Text>
      <Text style={styles.sectionTitle}>Connect to your ShopStops gateway</Text>
      <Text style={styles.helpText}>
        Use the public HTTPS gateway and a user session from your identity provider. Never enter
        the backend service token here.
      </Text>
      <TextInput
        accessibilityLabel="Gateway URL"
        autoCapitalize="none"
        autoCorrect={false}
        keyboardType="url"
        onChangeText={setGatewayUrl}
        placeholder="https://api.example.com/v1"
        placeholderTextColor="#94a3b8"
        style={styles.input}
        value={gatewayUrl}
      />
      <TextInput
        accessibilityLabel="Session token"
        autoCapitalize="none"
        autoCorrect={false}
        onChangeText={setSessionToken}
        placeholder="User session token (held in memory only)"
        placeholderTextColor="#94a3b8"
        secureTextEntry
        style={styles.input}
        value={sessionToken}
      />
    </View>
  );
  const activeStop = route?.stops
    .slice()
    .sort((a, b) => a.sequence - b.sequence)
    .find((stop) => stop.status !== "completed");

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={styles.screen}
    >
      <ScrollView contentContainerStyle={styles.content} keyboardShouldPersistTaps="handled">
        <View style={styles.header}>
          <View style={styles.brandLockup}>
            <Image accessible={false} source={require("../assets/icon.png")} style={styles.brandIcon} />
            <View>
              <Text style={styles.brand}>ShopStops</Text>
              <Text style={styles.tagline}>Neighborhood delivery, connected.</Text>
            </View>
          </View>
          <View style={styles.livePill}>
            <View style={styles.liveDot} />
            <Text style={styles.liveText}>BETA</Text>
          </View>
        </View>

        <View style={styles.hero}>
          <Text style={styles.heroEyebrow}>ONE CHECKOUT. MORE LOCAL.</Text>
          <Text style={styles.heroTitle}>Your neighborhood, on the way.</Text>
          <Text style={styles.heroCopy}>
            One local delivery for the restaurant and the shop next door.
          </Text>
          <View style={styles.heroStats}>
            <View><Text style={styles.statValue}>500m</Text><Text style={styles.statLabel}>local picks</Text></View>
            <View><Text style={styles.statValue}>2</Text><Text style={styles.statLabel}>stores per basket</Text></View>
            <View><Text style={styles.statValue}>1</Text><Text style={styles.statLabel}>shared route</Text></View>
          </View>
        </View>

        <View style={styles.rolePicker}>
          {(["Shopper", "Driver", "Merchant"] as const).map((item) => (
            <Pressable
              accessibilityRole="tab"
              accessibilityState={{ selected: role === item }}
              key={item}
              onPress={() => {
                setRole(item);
                setMessage("");
              }}
              style={[styles.roleButton, role === item && styles.roleButtonActive]}
            >
              <Text style={[styles.roleText, role === item && styles.roleTextActive]}>{item}</Text>
            </Pressable>
          ))}
        </View>

        <View style={styles.connectionCard}>
          <View style={styles.connectionHeader}>
            <View style={styles.connectionCopy}>
              <Text style={styles.sectionEyebrow}>SECURE CONNECTION</Text>
              <Text style={styles.connectionStatus}>
                {gatewayUrl && sessionToken ? "Gateway details entered" : "Connect to your gateway"}
              </Text>
            </View>
            <Pressable
              accessibilityRole="button"
              accessibilityState={{ expanded: connectionOpen }}
              onPress={() => setConnectionOpen((open) => !open)}
              style={styles.connectionToggle}
            >
              <Text style={styles.connectionToggleText}>{connectionOpen ? "Hide" : "Edit"}</Text>
            </Pressable>
          </View>
          {connectionOpen ? sharedForm : null}
        </View>

        {message ? (
          <View style={styles.messageBox} accessibilityLiveRegion="polite">
            <Text style={styles.messageText}>{message}</Text>
          </View>
        ) : null}
        {busy ? <ActivityIndicator color="#e5653d" style={styles.loader} /> : null}

        {role === "Shopper" ? (
          <>
            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>01 / DISCOVER</Text>
              <Text style={styles.sectionTitle}>Good things, close by.</Text>
              <Text style={styles.helpText}>
                Find subscription-active grocery and retail partners within 500 meters of your
                restaurant.
              </Text>
              <Field
                editable={!checkout && !orderIds.trim()}
                label="Anchor restaurant store ID"
                onChangeText={(value) => {
                  checkoutAttempt.current = null;
                  setAnchorStore(value);
                  setCatalogStoreID(value);
                }}
                value={anchorStore}
              />
              <View style={styles.actionRow}>
                <ActionButton disabled={busy} label="Find nearby shops" onPress={() => void findNearby()} />
                <ActionButton
                  disabled={busy}
                  label="Browse anchor catalog"
                  onPress={() => void loadCatalog(anchorStore)}
                  secondary
                />
              </View>
              {nearby.map((merchant) => (
                <View key={merchant.merchantId}>
                  <View style={styles.resultRow}>
                    <View style={styles.resultIcon}><Text style={styles.resultIconText}>+</Text></View>
                    <View style={styles.resultCopy}>
                      <Text style={styles.resultTitle}>{merchant.name}</Text>
                      <Text style={styles.resultSub}>{merchant.merchantType} · {Math.round(merchant.distanceMeters)} m away</Text>
                    </View>
                  </View>
                  <ActionButton
                    disabled={busy}
                    label={`Browse ${merchant.name}`}
                    onPress={() => void loadCatalog(merchant.storeLocationId)}
                    secondary
                  />
                </View>
              ))}
            </View>

            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>02 / BUILD YOUR ORDER</Text>
              <Text style={styles.sectionTitle}>Choose items from the block.</Text>
              <Field
                editable={!checkout}
                label="Store location ID"
                onChangeText={(value) => {
                  setCatalogStoreID(value);
                  setCatalog([]);
                  setQuantities({});
                }}
                value={catalogStoreID}
              />
              <ActionButton disabled={busy || Boolean(checkout)} label="Load store catalog" onPress={() => void loadCatalog()} />
              {catalog.map((item) => {
                const quantity = quantities[item.id] ?? 0;
                return (
                  <View key={item.id} style={styles.catalogRow}>
                    <View style={styles.resultCopy}>
                      <Text style={styles.resultTitle}>{item.name}</Text>
                      {item.description ? <Text style={styles.resultSub}>{item.description}</Text> : null}
                      <Text style={styles.resultSub}>
                        ${(item.unitPriceCents / 100).toFixed(2)}
                      </Text>
                    </View>
                    <View style={styles.quantityControl}>
                      <Pressable
                        accessibilityLabel={`Decrease ${item.name} quantity`}
                        accessibilityRole="button"
                        disabled={quantity === 0}
                        onPress={() => setQuantities((current) => ({
                          ...current,
                          [item.id]: Math.max(0, (current[item.id] ?? 0) - 1),
                        }))}
                        style={styles.quantityButton}
                      ><Text style={styles.quantityButtonText}>−</Text></Pressable>
                      <Text style={styles.quantityText}>{quantity}</Text>
                      <Pressable
                        accessibilityLabel={`Increase ${item.name} quantity`}
                        accessibilityRole="button"
                        disabled={quantity >= 99}
                        onPress={() => setQuantities((current) => ({
                          ...current,
                          [item.id]: (current[item.id] ?? 0) + 1,
                        }))}
                        style={styles.quantityButton}
                      ><Text style={styles.quantityButtonText}>+</Text></Pressable>
                    </View>
                  </View>
                );
              })}
              <View style={styles.fieldGrid}>
                <Field editable={!checkout && !orderIds.trim()} label="Drop-off latitude" keyboardType="decimal-pad" onChangeText={setLatitude} value={latitude} />
                <Field editable={!checkout && !orderIds.trim()} label="Drop-off longitude" keyboardType="decimal-pad" onChangeText={setLongitude} value={longitude} />
              </View>
              <ActionButton
                disabled={busy || Boolean(checkout) || catalog.length === 0}
                label="Create order from selected items"
                onPress={() => void createCustomerOrder()}
              />
            </View>

            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>03 / CHECKOUT</Text>
              <Text style={styles.sectionTitle}>Bring the whole block together.</Text>
              <Text style={styles.helpText}>
                Create one order from each store catalog you want to include, then create one unified checkout.
              </Text>
              <Text style={styles.tinyNote}>
                Delivery window · {new Date(deliveryWindow.start).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}
                {" – "}
                {new Date(deliveryWindow.end).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}
              </Text>
              <Field
                editable={!checkout}
                label="Order IDs (comma separated)"
                onChangeText={(value) => {
                  checkoutAttempt.current = null;
                  setOrderIds(value);
                }}
                value={orderIds}
              />
              <ActionButton
                disabled={busy || Boolean(checkout)}
                label={checkout ? "Basket created" : "Create basket"}
                onPress={() => void createCheckout()}
              />
              {checkout ? (
                <View style={styles.checkoutBox}>
                  <Text style={styles.checkoutLabel}>BASKET {checkout.id}</Text>
                  {checkout.invoiceLines.map((line, index) => (
                    <View key={`${line.type}-${index}`} style={styles.invoiceRow}>
                      <Text style={styles.invoiceLabel}>{line.type.replaceAll("_", " ")}</Text>
                      <Text style={styles.invoiceAmount}>
                        {(line.amountCents / 100).toLocaleString("en-US", {
                          style: "currency",
                          currency: checkout.currency.toUpperCase(),
                        })}
                      </Text>
                    </View>
                  ))}
                  <View style={[styles.invoiceRow, styles.totalRow]}>
                    <Text style={styles.totalLabel}>Total</Text>
                    <Text style={styles.totalValue}>
                      {(checkout.totalCents / 100).toLocaleString("en-US", {
                        style: "currency",
                        currency: checkout.currency.toUpperCase(),
                      })}
                    </Text>
                  </View>
                  <ActionButton
                    disabled={busy || !stripeKeyConfigured}
                    label={stripeKeyConfigured ? "Continue to secure payment" : "Add Stripe publishable key to pay"}
                    onPress={() => void payCheckout()}
                  />
                  <Text style={styles.tinyNote}>Checkout ID · {checkout.id}</Text>
                </View>
              ) : null}
              {basketWindow.current || orderIds.trim() || checkout ? (
                <ActionButton
                  disabled={busy}
                  label="Start a new basket"
                  onPress={startNewBasket}
                  secondary
                />
              ) : null}
            </View>
          </>
        ) : null}

        {role === "Driver" ? (
          <View style={styles.card}>
            <Text style={styles.sectionEyebrow}>DRIVER / ACTIVE DELIVERY</Text>
            <Text style={styles.sectionTitle}>Every stop, in the right order.</Text>
            <Text style={styles.helpText}>
              Load your assigned route and share location every five seconds while this screen is
              open. Stop sequencing is controlled by the server.
            </Text>
            <Field label="Assigned route ID" onChangeText={setRouteId} value={routeId} />
            <ActionButton disabled={busy} label="Load route" onPress={() => void loadRoute()} />
            {route ? (
              <View style={styles.routeBox}>
                <Text style={styles.checkoutLabel}>ROUTE · {route.status.toUpperCase()}</Text>
                {route.stops
                  .slice()
                  .sort((a, b) => a.sequence - b.sequence)
                  .map((stop, index) => (
                    <View key={stop.id} style={styles.stopRow}>
                      <View style={[styles.stopNumber, index === route.stops.length - 1 && styles.stopNumberLast]}>
                        <Text style={styles.stopNumberText}>{stop.sequence}</Text>
                      </View>
                      <View style={styles.resultCopy}>
                        <Text style={styles.resultTitle}>{stop.type === "dropoff" ? "Customer drop-off" : `Pickup ${stop.sequence}`}</Text>
                        <Text style={styles.resultSub}>{stop.status} · {stop.latitude.toFixed(4)}, {stop.longitude.toFixed(4)}</Text>
                      </View>
                    </View>
                  ))}
                {activeStop ? (
                  <View style={styles.stopActions}>
                    {activeStop.status === "pending" ? (
                      <ActionButton
                        disabled={busy}
                        label="Confirm arrival"
                        onPress={() => void updateRouteStop(activeStop.id, "arrive")}
                      />
                    ) : (
                      <ActionButton
                        disabled={busy}
                        label={activeStop.type === "dropoff" ? "Confirm delivery" : "Confirm pickup"}
                        onPress={() => void updateRouteStop(activeStop.id, "complete")}
                      />
                    )}
                  </View>
                ) : (
                  <Text style={styles.tinyNote}>All route stops are complete.</Text>
                )}
                <Text style={styles.tinyNote}>Routing matrix · {route.routingProvider}</Text>
              </View>
            ) : null}
            <ActionButton
              disabled={tracking || !route}
              label={tracking ? "Location sharing is active" : "Start 5-second location sharing"}
              onPress={() => void beginTracking()}
            />
            {tracking ? <ActionButton label="End location sharing" onPress={endTracking} secondary /> : null}
          </View>
        ) : null}

        {role === "Merchant" ? (
          <>
            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>MERCHANT / CATALOG</Text>
              <Text style={styles.sectionTitle}>Manage your store items.</Text>
              <Text style={styles.helpText}>
                Set prices and delivery volume. Items you mark unavailable disappear from customer catalogs; previous order prices remain unchanged.
              </Text>
              <ActionButton
                disabled={busy}
                label="Load my store locations"
                onPress={() => void loadMerchantStoreLocations()}
                secondary
              />
              {merchantStoreLocations.map((location) => (
                <Pressable
                  accessibilityRole="radio"
                  accessibilityState={{ selected: merchantStoreLocation === location.id }}
                  key={location.id}
                  onPress={() => setMerchantStoreLocation(location.id)}
                  style={[
                    styles.locationChoice,
                    merchantStoreLocation === location.id && styles.locationChoiceActive,
                  ]}
                >
                  <Text style={styles.resultTitle}>{location.name}</Text>
                  <Text style={styles.resultSub}>{location.id}</Text>
                </Pressable>
              ))}
              {merchantStoreLocations.length === 0 ? (
                <Text style={styles.tinyNote}>Load store locations before creating catalog items.</Text>
              ) : null}
              <Field label="Item name" onChangeText={setNewItemName} value={newItemName} />
              <Field label="Description" onChangeText={setNewItemDescription} value={newItemDescription} />
              <View style={styles.fieldGrid}>
                <Field label="Price (cents)" keyboardType="decimal-pad" onChangeText={setNewItemPrice} value={newItemPrice} />
                <Field label="Volume (liters)" keyboardType="decimal-pad" onChangeText={setNewItemVolume} value={newItemVolume} />
              </View>
              <ActionButton
                disabled={busy}
                label="Add catalog item"
                onPress={() => void createMerchantCatalogItem()}
              />
              <ActionButton
                disabled={busy}
                label="Refresh my catalog"
                onPress={() => void loadMerchantCatalog()}
                secondary
              />
              {merchantCatalog.length === 0 ? (
                <Text style={styles.tinyNote}>No catalog items loaded. Refresh to view items for your merchant account.</Text>
              ) : null}
              {merchantCatalog.map((item) => {
                const draft = merchantCatalogDrafts[item.id];
                if (!draft) return null;
                return (
                  <View key={item.id} style={styles.merchantItem}>
                    <Text style={styles.resultSub}>Store · {item.storeLocationId}</Text>
                    <Field
                      label="Item name"
                      onChangeText={(value) => setMerchantCatalogDrafts((current) => ({
                        ...current, [item.id]: { ...current[item.id], name: value },
                      }))}
                      value={draft.name}
                    />
                    <Field
                      label="Description"
                      onChangeText={(value) => setMerchantCatalogDrafts((current) => ({
                        ...current, [item.id]: { ...current[item.id], description: value },
                      }))}
                      value={draft.description}
                    />
                    <View style={styles.fieldGrid}>
                      <Field
                        label="Price (cents)"
                        keyboardType="decimal-pad"
                        onChangeText={(value) => setMerchantCatalogDrafts((current) => ({
                          ...current, [item.id]: { ...current[item.id], unitPriceCents: value },
                        }))}
                        value={draft.unitPriceCents}
                      />
                      <Field
                        label="Volume (liters)"
                        keyboardType="decimal-pad"
                        onChangeText={(value) => setMerchantCatalogDrafts((current) => ({
                          ...current, [item.id]: { ...current[item.id], volumeLiters: value },
                        }))}
                        value={draft.volumeLiters}
                      />
                    </View>
                    <ActionButton
                      disabled={busy}
                      label="Save item changes"
                      onPress={() => void updateMerchantCatalogItem(item)}
                    />
                    <ActionButton
                      disabled={busy}
                      label={item.available ? "Hide from customer catalogs" : "Make available to customers"}
                      onPress={() => void toggleMerchantCatalogAvailability(item)}
                      secondary
                    />
                  </View>
                );
              })}
            </View>
            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>MERCHANT / SUBSCRIPTION</Text>
              <Text style={styles.sectionTitle}>Your storefront, in good standing.</Text>
              <Text style={styles.helpText}>
                Manage the flat $199 monthly ShopStops subscription. A successful invoice activates
                storefront visibility.
              </Text>
              <View style={styles.actionRow}>
                <ActionButton disabled={busy} label="Refresh billing status" onPress={() => void refreshSubscription()} />
                <ActionButton disabled={busy || !stripeKeyConfigured} label="Save payment method" onPress={() => void setupMerchantPaymentMethod()} secondary />
              </View>
              {subscription ? (
                <View style={styles.statusCard}>
                  <Text style={styles.statusWord}>{subscription.status.replaceAll("_", " ")}</Text>
                  <Text style={styles.resultSub}>
                    {subscription.storefrontVisible ? "Storefront visible" : "Storefront hidden"}
                    {" · "}
                    {subscription.paymentMethodReady ? "Payment method saved" : "Payment method needed"}
                  </Text>
                  {subscription.currentPeriodEnd ? (
                    <Text style={styles.resultSub}>Next billing period ends {new Date(subscription.currentPeriodEnd).toLocaleDateString()}</Text>
                  ) : null}
                </View>
              ) : null}
              <ActionButton
                disabled={busy || !subscription?.paymentMethodReady}
                label="Start $199 monthly subscription"
                onPress={() => void startSubscription()}
              />
              <Text style={styles.tinyNote}>USD · billed monthly through Stripe</Text>
            </View>
          </>
        ) : null}

        <Text style={styles.footer}>
          Securely connected through your identity gateway · ShopStops {new Date().getFullYear()}
        </Text>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

function Field({
  label,
  value,
  onChangeText,
  keyboardType = "default",
  editable = true,
}: {
  label: string;
  value: string;
  onChangeText: (value: string) => void;
  keyboardType?: "default" | "decimal-pad";
  editable?: boolean;
}) {
  return (
    <View style={styles.field}>
      <Text style={styles.fieldLabel}>{label}</Text>
      <TextInput
        accessibilityLabel={label}
        autoCapitalize="none"
        autoCorrect={false}
        editable={editable}
        keyboardType={keyboardType}
        onChangeText={onChangeText}
        placeholder={label}
        placeholderTextColor="#94a3b8"
        style={styles.input}
        value={value}
      />
    </View>
  );
}

function ActionButton({
  label,
  onPress,
  disabled = false,
  secondary = false,
}: {
  label: string;
  onPress: () => void;
  disabled?: boolean;
  secondary?: boolean;
}) {
  return (
    <Pressable
      accessibilityRole="button"
      disabled={disabled}
      onPress={onPress}
      style={[
        styles.actionButton,
        secondary && styles.actionButtonSecondary,
        disabled && styles.actionButtonDisabled,
      ]}
    >
      <Text style={[styles.actionText, secondary && styles.actionTextSecondary]}>{label}</Text>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  screen: { flex: 1, backgroundColor: "#f6f5f1" },
  content: { width: "100%", maxWidth: 760, alignSelf: "center", padding: 20, paddingBottom: 64 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingVertical: 12, marginBottom: 18 },
  brandLockup: { flexDirection: "row", alignItems: "center", gap: 10 },
  brandIcon: { width: 42, height: 42, borderRadius: 12, backgroundColor: "#19352f" },
  brand: { color: "#19352f", fontSize: 21, fontWeight: "900", letterSpacing: -0.8 },
  tagline: { color: "#64756e", fontSize: 12, marginTop: 2 },
  livePill: { flexDirection: "row", alignItems: "center", borderRadius: 24, paddingHorizontal: 11, paddingVertical: 7, backgroundColor: "#e4efe8" },
  liveDot: { width: 7, height: 7, borderRadius: 4, backgroundColor: "#398260", marginRight: 6 },
  liveText: { color: "#28694d", fontSize: 10, fontWeight: "800", letterSpacing: 1 },
  hero: { backgroundColor: "#19352f", borderRadius: 22, padding: 24, overflow: "hidden" },
  heroEyebrow: { color: "#f6a17e", fontSize: 10, fontWeight: "800", letterSpacing: 1.7 },
  heroTitle: { color: "#fffdf7", fontSize: 31, fontWeight: "800", lineHeight: 37, marginTop: 10, maxWidth: 430 },
  heroCopy: { color: "#c3d2cb", fontSize: 14, lineHeight: 21, marginTop: 8, maxWidth: 390 },
  heroStats: { flexDirection: "row", justifyContent: "space-between", marginTop: 24, paddingTop: 17, borderTopWidth: 1, borderTopColor: "#436057" },
  statValue: { color: "#fffdf7", fontSize: 19, fontWeight: "800" },
  statLabel: { color: "#afc4bb", fontSize: 10, marginTop: 3 },
  rolePicker: { flexDirection: "row", gap: 7, padding: 5, backgroundColor: "#e9e8e2", borderRadius: 14, marginTop: 18 },
  roleButton: { flex: 1, alignItems: "center", paddingVertical: 11, borderRadius: 10 },
  roleButtonActive: { backgroundColor: "#fff", elevation: 1 },
  roleText: { color: "#64756e", fontSize: 12, fontWeight: "700" },
  roleTextActive: { color: "#19352f" },
  connectionCard: { backgroundColor: "#fff", borderRadius: 16, padding: 15, marginTop: 14, borderWidth: 1, borderColor: "#ecece7" },
  connectionHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  connectionCopy: { flex: 1 },
  connectionStatus: { color: "#19352f", fontSize: 14, fontWeight: "800", marginTop: 5 },
  connectionToggle: { paddingVertical: 9, paddingHorizontal: 13, backgroundColor: "#e9f0ec", borderRadius: 9 },
  connectionToggleText: { color: "#24443b", fontSize: 12, fontWeight: "800" },
  configCard: { backgroundColor: "#fff", borderRadius: 18, padding: 0, marginTop: 13 },
  card: { backgroundColor: "#fff", borderRadius: 18, padding: 20, marginTop: 14, borderWidth: 1, borderColor: "#ecece7", shadowColor: "#19352f", shadowOpacity: 0.04, shadowRadius: 12, shadowOffset: { width: 0, height: 4 }, elevation: 1 },
  sectionEyebrow: { color: "#e5653d", fontSize: 10, fontWeight: "800", letterSpacing: 1.4 },
  sectionTitle: { color: "#19352f", fontSize: 20, fontWeight: "800", marginTop: 7 },
  helpText: { color: "#65756f", fontSize: 13, lineHeight: 19, marginTop: 6, marginBottom: 11 },
  field: { flex: 1, minWidth: 125, marginTop: 6 },
  fieldLabel: { color: "#52645e", fontSize: 11, fontWeight: "700", marginBottom: 6 },
  fieldGrid: { flexDirection: "row", flexWrap: "wrap", gap: 10 },
  input: { backgroundColor: "#f8f9f7", borderColor: "#e4e9e5", borderWidth: 1, borderRadius: 10, color: "#203b34", fontSize: 13, paddingHorizontal: 12, paddingVertical: 11, marginTop: 8 },
  actionButton: { backgroundColor: "#e5653d", minHeight: 45, justifyContent: "center", alignItems: "center", borderRadius: 11, paddingHorizontal: 15, marginTop: 13 },
  actionButtonSecondary: { backgroundColor: "#e9f0ec" },
  actionButtonDisabled: { opacity: 0.48 },
  actionText: { color: "#fff", fontWeight: "800", fontSize: 12, textAlign: "center" },
  actionTextSecondary: { color: "#24443b" },
  actionRow: { gap: 3 },
  messageBox: { padding: 13, borderRadius: 11, backgroundColor: "#fff1e9", marginTop: 12 },
  messageText: { color: "#8e4229", fontSize: 12, lineHeight: 18 },
  loader: { marginTop: 12 },
  resultRow: { flexDirection: "row", alignItems: "center", paddingVertical: 12, borderTopWidth: 1, borderTopColor: "#edf0ed", marginTop: 12 },
  resultIcon: { width: 38, height: 38, borderRadius: 12, backgroundColor: "#e7f1eb", justifyContent: "center", alignItems: "center", marginRight: 11 },
  resultIconText: { color: "#397457", fontSize: 22, fontWeight: "500" },
  resultCopy: { flex: 1 },
  resultTitle: { color: "#213b34", fontWeight: "800", fontSize: 13 },
  resultSub: { color: "#73817b", fontSize: 11, marginTop: 4 },
  catalogRow: { flexDirection: "row", alignItems: "center", paddingVertical: 12, borderTopWidth: 1, borderTopColor: "#edf0ed", marginTop: 10 },
  merchantItem: { backgroundColor: "#f8f9f7", borderRadius: 13, padding: 14, marginTop: 14 },
  locationChoice: { borderWidth: 1, borderColor: "#e4e9e5", borderRadius: 10, padding: 11, marginTop: 8 },
  locationChoiceActive: { borderColor: "#397457", backgroundColor: "#e7f1eb" },
  quantityControl: { flexDirection: "row", alignItems: "center", gap: 10, marginLeft: 10 },
  quantityButton: { width: 30, height: 30, borderRadius: 15, backgroundColor: "#e9f0ec", alignItems: "center", justifyContent: "center" },
  quantityButtonText: { color: "#24443b", fontSize: 17, fontWeight: "800" },
  quantityText: { minWidth: 16, color: "#213b34", textAlign: "center", fontWeight: "800" },
  checkoutBox: { backgroundColor: "#f8f9f7", borderRadius: 13, padding: 14, marginTop: 16 },
  routeBox: { backgroundColor: "#f8f9f7", borderRadius: 13, padding: 14, marginTop: 14 },
  stopActions: { marginTop: 3 },
  checkoutLabel: { color: "#77857f", fontSize: 9, fontWeight: "800", letterSpacing: 1.2, marginBottom: 11 },
  invoiceRow: { flexDirection: "row", justifyContent: "space-between", paddingVertical: 7 },
  invoiceLabel: { color: "#5f706a", fontSize: 12, textTransform: "capitalize" },
  invoiceAmount: { color: "#213b34", fontSize: 12, fontWeight: "700" },
  totalRow: { borderTopWidth: 1, borderTopColor: "#dfe6e1", marginTop: 4, paddingTop: 11 },
  totalLabel: { color: "#203b34", fontWeight: "800", fontSize: 13 },
  totalValue: { color: "#203b34", fontWeight: "900", fontSize: 15 },
  tinyNote: { color: "#829089", fontSize: 10, marginTop: 9, lineHeight: 15 },
  stopRow: { flexDirection: "row", alignItems: "center", paddingVertical: 9 },
  stopNumber: { width: 28, height: 28, borderRadius: 14, backgroundColor: "#e5653d", justifyContent: "center", alignItems: "center", marginRight: 11 },
  stopNumberLast: { backgroundColor: "#397457" },
  stopNumberText: { color: "#fff", fontWeight: "800", fontSize: 11 },
  statusCard: { backgroundColor: "#f8f9f7", padding: 15, borderRadius: 12, marginTop: 15 },
  statusWord: { color: "#24443b", fontWeight: "900", fontSize: 18, textTransform: "capitalize", marginBottom: 5 },
  footer: { color: "#8b9892", textAlign: "center", fontSize: 10, marginTop: 25, lineHeight: 15 },
});
