package interfaces

// Observer defines the interface for observers that want to be notified of events
type Observer[T any] interface {
	// OnEvent is called when an event occurs
	OnEvent(event T)
}

// Subject defines the interface for subjects that can be observed
type Subject[T any] interface {
	// Subscribe adds an observer to the list of observers
	Subscribe(observer Observer[T])

	// Unsubscribe removes an observer from the list of observers
	Unsubscribe(observer Observer[T])

	// NotifyObservers notifies all registered observers of an event
	NotifyObservers(event T)
}