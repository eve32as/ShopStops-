import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import MapView, {
  Marker,
  Polyline,
  type LatLng,
  type Region,
} from "react-native-maps";
import { Pressable, StyleSheet, Text, View } from "react-native";

export type MapCoordinate = LatLng;

export type DeliveryMapMarker = {
  id: string;
  coordinate: MapCoordinate;
  title: string;
  description?: string;
  kind: "driver" | "pickup" | "dropoff" | "stop";
  active?: boolean;
  sequence?: number;
};

type DeliveryMapProps = {
  accessibleLabel: string;
  coordinates: MapCoordinate[];
  markers: DeliveryMapMarker[];
  emptyMessage: string;
  fitKey: string;
};

const validCoordinate = (point: MapCoordinate) =>
  Number.isFinite(point.latitude) &&
  Number.isFinite(point.longitude) &&
  point.latitude >= -90 &&
  point.latitude <= 90 &&
  point.longitude >= -180 &&
  point.longitude <= 180;

export function DeliveryMap({
  accessibleLabel,
  coordinates,
  markers,
  emptyMessage,
  fitKey,
}: DeliveryMapProps) {
  const mapRef = useRef<MapView>(null);
  const mapReady = useRef(false);
  const fittedKey = useRef("");
  const [mapReadyState, setMapReadyState] = useState(false);
  const [mapInitializationFailed, setMapInitializationFailed] = useState(false);
  const routeCoordinates = useMemo(() => {
    const validPoints = coordinates.filter(validCoordinate);
    if (validPoints.length <= 500) return validPoints;
    const stride = Math.ceil((validPoints.length - 1) / 499);
    return validPoints.filter((_, index) =>
      index % stride === 0 || index === validPoints.length - 1);
  }, [coordinates]);
  const visibleMarkers = useMemo(
    () => markers.filter((marker) => validCoordinate(marker.coordinate)),
    [markers],
  );
  const fitCoordinates = useMemo(() => {
    const points = [...routeCoordinates, ...visibleMarkers.map((marker) => marker.coordinate)];
    return points.filter(validCoordinate);
  }, [routeCoordinates, visibleMarkers]);

  const fitMap = useCallback((animated: boolean) => {
    const map = mapRef.current;
    if (!map || fitCoordinates.length === 0) return;
    const latitudeSpan = Math.max(...fitCoordinates.map((point) => point.latitude)) -
      Math.min(...fitCoordinates.map((point) => point.latitude));
    const longitudeSpan = Math.max(...fitCoordinates.map((point) => point.longitude)) -
      Math.min(...fitCoordinates.map((point) => point.longitude));
    if (fitCoordinates.length === 1 || (latitudeSpan < 0.0001 && longitudeSpan < 0.0001)) {
      const point = fitCoordinates[0];
      const region: Region = {
        ...point,
        latitudeDelta: 0.015,
        longitudeDelta: 0.015,
      };
      map.animateToRegion(region, animated ? 350 : 0);
      return;
    }
    map.fitToCoordinates(fitCoordinates, {
      animated,
      edgePadding: { top: 56, right: 48, bottom: 56, left: 48 },
    });
  }, [fitCoordinates]);

  useEffect(() => {
    if (!mapReady.current || fittedKey.current === fitKey || fitCoordinates.length === 0) return;
    fittedKey.current = fitKey;
    fitMap(false);
  }, [fitCoordinates.length, fitKey, fitMap]);

  const hasCoordinates = fitCoordinates.length > 0;
  useEffect(() => {
    if (!hasCoordinates) return;
    const timer = setTimeout(() => {
      if (!mapReady.current) setMapInitializationFailed(true);
    }, 12_000);
    return () => clearTimeout(timer);
  }, [hasCoordinates]);

  const onMapReady = useCallback(() => {
    mapReady.current = true;
    setMapReadyState(true);
    setMapInitializationFailed(false);
    if (fitCoordinates.length > 0) {
      fittedKey.current = fitKey;
      fitMap(false);
    }
  }, [fitCoordinates.length, fitKey, fitMap]);

  if (fitCoordinates.length === 0) {
    return (
      <View accessibilityLabel={accessibleLabel} style={styles.emptyMap}>
        <Text style={styles.emptyMapTitle}>Map unavailable</Text>
        <Text style={styles.emptyMapMessage}>{emptyMessage}</Text>
      </View>
    );
  }

  return (
    <View accessibilityLabel={accessibleLabel} style={styles.mapFrame}>
      <MapView
        ref={mapRef}
        accessibilityLabel={accessibleLabel}
        onMapReady={onMapReady}
        style={StyleSheet.absoluteFill}
      >
        {routeCoordinates.length > 1 ? (
          <Polyline
            coordinates={routeCoordinates}
            strokeColor="#397457"
            strokeWidth={5}
            lineCap="round"
            lineJoin="round"
          />
        ) : null}
        {visibleMarkers.map((marker) => (
          <Marker
            key={marker.id}
            coordinate={marker.coordinate}
            title={marker.title}
            description={marker.description}
            tracksViewChanges={false}
          >
            <View
              style={[
                styles.marker,
                marker.kind === "driver" && styles.driverMarker,
                marker.kind === "pickup" && styles.pickupMarker,
                marker.kind === "dropoff" && styles.dropoffMarker,
                marker.kind === "stop" && styles.stopMarker,
                marker.active && styles.activeMarker,
              ]}
            >
              <Text style={styles.markerLabel}>
                {marker.kind === "driver" ? "D" : marker.sequence ?? (marker.kind === "pickup" ? "P" : "D")}
              </Text>
            </View>
          </Marker>
        ))}
      </MapView>
      {!mapReadyState ? (
        <View pointerEvents="none" style={styles.mapStatus}>
          <Text style={styles.mapStatusText}>
            {mapInitializationFailed
              ? "Map could not initialize. Check the app build and map configuration."
              : "Loading map…"}
          </Text>
        </View>
      ) : null}
      <Pressable
        accessibilityRole="button"
        accessibilityLabel="Fit route on map"
        onPress={() => fitMap(true)}
        style={styles.fitButton}
      >
        <Text style={styles.fitButtonText}>Fit route</Text>
      </Pressable>
    </View>
  );
}

const styles = StyleSheet.create({
  mapFrame: {
    height: 250,
    marginTop: 12,
    borderRadius: 14,
    overflow: "hidden",
    backgroundColor: "#e8eee9",
  },
  emptyMap: {
    minHeight: 150,
    marginTop: 12,
    padding: 18,
    borderRadius: 14,
    backgroundColor: "#edf2ee",
    justifyContent: "center",
  },
  emptyMapTitle: { color: "#19352f", fontSize: 14, fontWeight: "800" },
  emptyMapMessage: { color: "#60746a", fontSize: 12, lineHeight: 18, marginTop: 5 },
  marker: {
    width: 32,
    height: 32,
    alignItems: "center",
    justifyContent: "center",
    borderRadius: 16,
    borderWidth: 2,
    borderColor: "#fff",
    elevation: 4,
  },
  driverMarker: { backgroundColor: "#19352f" },
  pickupMarker: { backgroundColor: "#397457" },
  dropoffMarker: { backgroundColor: "#e5653d" },
  stopMarker: { backgroundColor: "#64748b" },
  activeMarker: { borderColor: "#f4ca59", borderWidth: 4 },
  markerLabel: { color: "#fff", fontSize: 11, fontWeight: "900" },
  mapStatus: {
    position: "absolute",
    top: 10,
    left: 10,
    right: 10,
    padding: 8,
    borderRadius: 8,
    backgroundColor: "#fff",
  },
  mapStatusText: { color: "#52675d", fontSize: 11, fontWeight: "700" },
  fitButton: {
    position: "absolute",
    right: 10,
    bottom: 10,
    paddingHorizontal: 12,
    paddingVertical: 9,
    borderRadius: 18,
    backgroundColor: "#fff",
    elevation: 3,
  },
  fitButtonText: { color: "#19352f", fontSize: 11, fontWeight: "800" },
});
