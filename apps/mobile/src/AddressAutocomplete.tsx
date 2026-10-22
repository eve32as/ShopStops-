import { useEffect, useRef, useState } from "react";
import { Pressable, StyleSheet, Text, TextInput, View } from "react-native";

type PlacePrediction = {
  placeId: string;
  description: string;
};

type SelectedAddress = {
  address: string;
  latitude: number;
  longitude: number;
};

type Props = {
  apiKey: string;
  disabled?: boolean;
  onAddressSelected: (address: SelectedAddress) => void;
};

function newSessionToken() {
  return globalThis.crypto?.randomUUID?.()
    ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export function AddressAutocomplete({ apiKey, disabled, onAddressSelected }: Props) {
  const [input, setInput] = useState("");
  const [predictions, setPredictions] = useState<PlacePrediction[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const sessionToken = useRef(newSessionToken());
  const selectionController = useRef<AbortController | null>(null);

  useEffect(() => {
    const query = input.trim();
    if (!apiKey || query.length < 3) {
      setPredictions([]);
      setError("");
      return;
    }
    const controller = new AbortController();
    const timer = setTimeout(() => {
      void fetch("https://places.googleapis.com/v1/places:autocomplete", {
        method: "POST",
        signal: controller.signal,
        headers: {
          "Content-Type": "application/json",
          "X-Goog-Api-Key": apiKey,
          "X-Goog-FieldMask": "suggestions.placePrediction.placeId,suggestions.placePrediction.text.text",
        },
        body: JSON.stringify({ input: query, sessionToken: sessionToken.current }),
      }).then(async (response) => {
        if (!response.ok) throw new Error("Google Places could not find address suggestions.");
        const result = await response.json() as {
          suggestions?: Array<{ placePrediction?: { placeId?: string; text?: { text?: string } } }>;
        };
        if (controller.signal.aborted) return;
        setPredictions((result.suggestions ?? []).flatMap((suggestion) => {
          const place = suggestion.placePrediction;
          return place?.placeId && place.text?.text
            ? [{ placeId: place.placeId, description: place.text.text }]
            : [];
        }));
        setError("");
      }).catch((requestError: unknown) => {
        if (controller.signal.aborted) return;
        setPredictions([]);
        setError(requestError instanceof Error ? requestError.message : "Address suggestions are unavailable.");
      });
    }, 300);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [apiKey, input]);

  useEffect(() => () => selectionController.current?.abort(), []);

  async function selectPrediction(prediction: PlacePrediction) {
    selectionController.current?.abort();
    const controller = new AbortController();
    selectionController.current = controller;
    setLoading(true);
    setError("");
    try {
      const response = await fetch(
        `https://places.googleapis.com/v1/places/${encodeURIComponent(prediction.placeId)}`,
        {
          signal: controller.signal,
          headers: {
            "X-Goog-Api-Key": apiKey,
            "X-Goog-FieldMask": "formattedAddress,location",
          },
        },
      );
      if (!response.ok) throw new Error("Google Places could not load that address.");
      const place = await response.json() as {
        formattedAddress?: string;
        location?: { latitude?: number; longitude?: number };
      };
      const address = place.formattedAddress;
      const latitude = place.location?.latitude;
      const longitude = place.location?.longitude;
      if (!address || !Number.isFinite(latitude) || !Number.isFinite(longitude)) {
        throw new Error("Google Places returned an address without valid coordinates.");
      }
      setInput(address);
      setPredictions([]);
      sessionToken.current = newSessionToken();
      onAddressSelected({ address, latitude: latitude!, longitude: longitude! });
    } catch (selectionError) {
      if (!controller.signal.aborted) {
        setError(selectionError instanceof Error ? selectionError.message : "Could not select that address.");
      }
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }

  return (
    <View style={styles.container}>
      <Text style={styles.label}>Drop-off address</Text>
      <TextInput
        accessibilityLabel="Drop-off address"
        autoComplete="street-address"
        editable={!disabled && !loading}
        onChangeText={setInput}
        placeholder={apiKey ? "Search for a delivery address" : "Configure Google Maps key to search"}
        style={styles.input}
        value={input}
      />
      {loading ? <Text style={styles.note}>Loading address…</Text> : null}
      {predictions.map((prediction) => (
        <Pressable
          accessibilityRole="button"
          key={prediction.placeId}
          onPress={() => void selectPrediction(prediction)}
          style={styles.prediction}
        >
          <Text style={styles.predictionText}>{prediction.description}</Text>
        </Pressable>
      ))}
      {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
      {predictions.length > 0 ? <Text style={styles.attribution}>Powered by Google</Text> : null}
      {!apiKey ? <Text style={styles.note}>Address search is optional; enter coordinates below.</Text> : null}
    </View>
  );
}

const styles = StyleSheet.create({
  container: { marginTop: 12, gap: 8 },
  label: { color: "#52645e", fontSize: 12, fontWeight: "700" },
  input: {
    backgroundColor: "#fff",
    borderColor: "#dbe3df",
    borderRadius: 10,
    borderWidth: 1,
    color: "#213b34",
    minHeight: 46,
    paddingHorizontal: 12,
  },
  prediction: { borderBottomColor: "#e7ece9", borderBottomWidth: 1, paddingVertical: 10 },
  predictionText: { color: "#213b34", fontSize: 13 },
  attribution: { color: "#77857f", fontSize: 10, textAlign: "right" },
  note: { color: "#77857f", fontSize: 11 },
  error: { color: "#a73939", fontSize: 12 },
});
