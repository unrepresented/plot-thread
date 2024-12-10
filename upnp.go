package plotthread

import (
	"gitlab.com/NebulousLabs/go-upnp"
)

// HandlePortForward manages port (un)forwarding using UPnP.
func HandlePortForward(port uint16, fwd bool) (string, bool, error) {
	// discover router
	router, err := upnp.Discover()
	if err != nil {
		return "", false, err
	}

	if fwd {
		// discover external IP
		ip, err := router.ExternalIP()
		if err != nil {
			return "", false, err
		}

		// forward the port
		err = router.Forward(port, "plotthread")
		if err != nil {
			return "", false, err
		}

		// check the port
		ok, err := router.IsForwardedTCP(port)
		if err != nil {
			return "", false, err
		}
		return ip, ok, nil
	}

	// clear the port
	err = router.Clear(port)
	if err != nil {
		return "", false, err
	}

	// check the port
	ok, err := router.IsForwardedTCP(port)
	if err != nil {
		return "", false, err
	}
	return "", !ok, nil
}
